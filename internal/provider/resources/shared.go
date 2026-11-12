package resources

import (
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
)

// systemCatalogProject is the project that holds the system-catalog SKU profiles.
const systemCatalogProject = "system-catalog"

// pollInterval is how often provisioning, actions and teardown are polled.
var pollInterval = 15 * time.Second

// SetPollIntervalForTesting changes the polling interval and returns a function
// that restores it. It exists so tests can run against a fast mock.
func SetPollIntervalForTesting(d time.Duration) (restore func()) {
	old := pollInterval
	pollInterval = d
	return func() { pollInterval = old }
}

// workflowErrors extracts workflowStatus.resources[].errors[].raw.
func workflowErrors(ws map[string]any) []string {
	var out []string
	resources, _ := ws["resources"].([]any)
	for _, res := range resources {
		rm, _ := res.(map[string]any)
		errs, _ := rm["errors"].([]any)
		for _, e := range errs {
			if em, ok := e.(map[string]any); ok {
				if raw, ok := em["raw"].(string); ok && raw != "" {
					out = append(out, raw)
				}
			}
		}
	}
	return out
}

// normalizedActionValue returns the comparable form of a desired_action value:
// "" for null/unknown, otherwise the literal string. "none" stays as "none" since
// it has explicit "do-nothing" semantics.
func normalizedActionValue(in types.String) string {
	if in.IsNull() || in.IsUnknown() {
		return ""
	}
	return in.ValueString()
}

// metadataFromServer merges the configured metadata with what the server returned.
// base carries the user's values (plan or prior state). Computed-and-optional
// fields keep the configured value when there is one, so that state matches the plan.
func metadataFromServer(base MetadataModel, srv apiv1.ObjectMeta, fromRead bool) MetadataModel {
	out := base
	out.Name = types.StringValue(srv.Name)
	if srv.Name == "" {
		out.Name = base.Name
	}
	// Project and workspace are echoed inconsistently by the API; the configured scope is authoritative.
	if fromRead || out.DisplayName.IsUnknown() || out.DisplayName.IsNull() {
		out.DisplayName = nullableString(srv.DisplayName)
	}
	if fromRead || out.Description.IsUnknown() || out.Description.IsNull() {
		out.Description = nullableString(srv.Description)
	}
	out.Labels = reconcileStringMap(base.Labels, srv.Labels)
	out.Annotations = reconcileStringMap(base.Annotations, srv.Annotations)
	out.CreatedBy = userMetaFromSDK(srv.CreatedBy)
	out.ModifiedBy = userMetaFromSDK(srv.ModifiedBy)
	return out
}

// reconcileStringMap returns what to store for an Optional+Computed map. The
// backend adds its own entries (for example paas.envmgmt.io/compute_type), so when
// the user configured a map the result is limited to the configured keys (taking
// the server value for each) and otherwise it is the full server map.
func reconcileStringMap(configured types.Map, server map[string]string) types.Map {
	if configured.IsNull() || configured.IsUnknown() {
		return mapFromStringMap(server)
	}
	out := map[string]string{}
	for k := range configured.Elements() {
		if v, ok := server[k]; ok {
			out[k] = v
		} else if sv, ok := configured.Elements()[k].(types.String); ok {
			out[k] = sv.ValueString()
		}
	}
	return mapFromStringMap(out)
}

// closestName suggests the entry of names nearest to name, or "".
func closestName(name string, names []string) string {
	best, bestDist := "", 4
	for _, n := range names {
		d := editDistance(strings.ToLower(name), strings.ToLower(n))
		if d < bestDist || (d == bestDist && best != "" && n < best) {
			best, bestDist = n, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}
