package resources

import (
	"context"
	"fmt"
	"strings"
	"time"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	typed "github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

// First-class objects (clusters, bare metal, virtual machines) each have a compute instance of the
// same name behind them. It is hidden from normal listings (the SDK asks for it with
// include_first_class=true) and holds what the first-class record does not: the workflow error and
// the provisioning log of a failure, and the teardown that outlives the record on delete.

// hiddenInstances returns the compute instance client for a project or workspace.
func hiddenInstances(pd *client.ProviderData, project, workspace string) typed.ComputeInstanceInterface {
	if workspace == "" {
		return pd.Clientset.V1alpha1().ComputeInstances(project)
	}
	return pd.Clientset.V1alpha1().Workspaces(project).ComputeInstances(workspace)
}

// maxListPages bounds how many pages listPages reads, so a server that keeps answering with a
// continue marker cannot loop forever.
const maxListPages = 50

// listPages reads a list page by page. fetch returns one page and the marker for the next one. It
// stops at an empty page, at an empty or repeated marker, or after maxListPages. Once a page has
// been read, a later failure keeps what was read: callers use this for best-effort details.
func listPages[T any](fetch func(opts gpupaas.ListOptions) (items []T, next string, err error)) ([]T, error) {
	var all []T
	opts := gpupaas.ListOptions{Limit: "100"}
	for page := 0; page < maxListPages; page++ {
		items, next, err := fetch(opts)
		if err != nil {
			if page > 0 {
				return all, nil
			}
			return nil, err
		}
		all = append(all, items...)
		if next == "" || len(items) == 0 || next == opts.Continue {
			break
		}
		opts.Continue = next
	}
	return all, nil
}

// listAllInstances reads every compute instance of a project or workspace.
func listAllInstances(ctx context.Context, ci typed.ComputeInstanceInterface) ([]apiv1.ComputeInstance, error) {
	return listPages(func(opts gpupaas.ListOptions) ([]apiv1.ComputeInstance, string, error) {
		list, err := ci.List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return list.Items, list.Metadata.Continue, nil
	})
}

// clusterOwning returns which of the clusters an instance belongs to: the longest cluster name that
// is the instance name, or a prefix of it followed by "-". A cluster "prod" must not claim the
// nodes of a cluster "prod-east". It returns "" when none matches.
func clusterOwning(instance string, clusters []string) string {
	owner := ""
	for _, c := range clusters {
		if c == "" || !(instance == c || strings.HasPrefix(instance, c+"-")) {
			continue
		}
		if len(c) > len(owner) {
			owner = c
		}
	}
	return owner
}

// hiddenInstanceFailure describes why the compute instance behind a first-class object failed: its
// workflow errors and a log excerpt. Best effort: it returns "" when nothing can be read.
func hiddenInstanceFailure(ctx context.Context, ci typed.ComputeInstanceInterface, name, reason string) string {
	inst, err := ci.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		return ""
	}
	var b strings.Builder
	errs := workflowErrors(inst.Status.WorkflowStatus)
	if len(errs) == 0 && inst.Status.Reason != "" && inst.Status.Reason != reason {
		errs = []string{inst.Status.Reason}
	}
	for _, e := range errs {
		fmt.Fprintf(&b, "\n  %s", truncate(e, 700))
	}
	if lg, lerr := ci.FailureLog(ctx, name, 12); lerr == nil && len(lg.Lines) > 0 {
		b.WriteString("\n  Log excerpt:")
		for _, line := range lg.Lines {
			fmt.Fprintf(&b, "\n    %s", truncate(line, 300))
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n\nWorkflow details:" + b.String()
}

// maxHiddenReadErrors is how many consecutive failed reads of a backing compute instance are
// tolerated before a wait gives up.
const maxHiddenReadErrors = 5

// maxStaleFailedReads is how many polls a compute instance may still read "failed" from before its teardown, while the
// action is not destroy, before the wait gives up. The record is gone but the instance keeps the status of the earlier
// failed provisioning until the destroy run starts; a failure of the destroy itself is reported at once.
const maxStaleFailedReads = 3

// waitForHiddenInstanceGone waits until the compute instance behind a deleted first-class object is
// gone. The record disappears first; the instance keeps "destroying" for a while and the platform
// refuses to delete the workspace until it is done, so a dependent workspace would fail to delete.
func waitForHiddenInstanceGone(ctx context.Context, pd *client.ProviderData, project, workspace, name string) error {
	return waitForInstanceGone(ctx, hiddenInstances(pd, project, workspace), name)
}

func waitForInstanceGone(ctx context.Context, ci typed.ComputeInstanceInterface, name string) error {
	errs, stale := 0, 0
	for {
		inst, err := ci.Get(ctx, name, gpupaas.GetOptions{})
		switch {
		case gpupaas.IsNotFound(err):
			return nil
		case err != nil:
			// Only a 404 means gone. A deadline or a run of failed reads must not report success while
			// the instance may still be tearing down: the object stays in state and a destroy retries.
			if ctx.Err() != nil {
				return fmt.Errorf("waiting for the compute instance of %q to be removed: %w", name, ctx.Err())
			}
			if errs++; errs >= maxHiddenReadErrors {
				return fmt.Errorf("cannot confirm that the compute instance of %q was removed (run terraform destroy again to retry): %w", name, err)
			}
		case inst.Status.Failed():
			if inst.Status.Phase() == apiv1.ComputeStatusFailed && !strings.EqualFold(inst.Status.Action, "destroy") {
				if stale++; stale <= maxStaleFailedReads {
					errs = 0
					break // the status of the earlier failed provisioning: the teardown has not started yet
				}
			}
			return fmt.Errorf("%q is gone but its compute instance ended %q: %s", name, inst.Status.Phase(), inst.Status.Reason)
		default:
			errs, stale = 0, 0
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for the compute instance of %q to be removed: %w", name, ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}
