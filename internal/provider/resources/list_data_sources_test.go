package resources

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

func TestListKindsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range listKinds {
		if seen[k.suffix] {
			t.Errorf("duplicate list data source %q", k.suffix)
		}
		seen[k.suffix] = true
		if len(k.fields) == 0 || k.fields[0].name != "name" {
			t.Errorf("%s: the first item field must be name, got %+v", k.suffix, k.fields)
		}
		if k.scoped {
			found := false
			for _, f := range k.fields {
				found = found || f.name == "workspace"
			}
			if !found {
				t.Errorf("%s takes a workspace argument, so its items must carry the workspace", k.suffix)
			}
		}
		if k.fetch == nil || k.title == "" {
			t.Errorf("%s: incomplete", k.suffix)
		}
	}
	if len(ListDataSources()) != len(listKinds) {
		t.Errorf("ListDataSources returns %d, want %d", len(ListDataSources()), len(listKinds))
	}
	// the names the platform's naming convention implies for the singular data sources, pluralised
	for _, want := range []string{"workspaces", "ssh_keys", "security_groups", "virtual_machines", "bare_metal_instances", "kubernetes_clusters",
		"high_speed_storages", "object_storages", "virtual_machine_skus", "bare_metal_skus", "kubernetes_skus"} {
		if !seen[want] {
			t.Errorf("missing list data source %q", want)
		}
	}
}

func TestListAllReadsEveryPageAndFailsWhenAnyPageFails(t *testing.T) {
	pages := map[string][]int{"": {1, 2}, "2": {3, 4}, "4": {5}}
	next := map[string]string{"": "2", "2": "4", "4": ""}
	got, err := listAll(func(opts gpupaas.ListOptions) ([]int, string, error) {
		return pages[opts.Continue], next[opts.Continue], nil
	})
	if err != nil || len(got) != 5 {
		t.Fatalf("listAll = %v, %v", got, err)
	}
	// a page that fails after others were read is an error, never a shorter list
	_, err = listAll(func(opts gpupaas.ListOptions) ([]int, string, error) {
		if opts.Continue == "2" {
			return nil, "", errors.New("boom")
		}
		return pages[opts.Continue], next[opts.Continue], nil
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("a failing page must fail the list, got %v", err)
	}
	// a marker that repeats ends the list; a list that never ends is an error
	if got, err := listAll(func(opts gpupaas.ListOptions) ([]int, string, error) { return []int{1}, "same", nil }); err != nil || len(got) != 2 {
		t.Fatalf("a repeated marker: %v %v", got, err)
	}
	n := 0
	if _, err := listAll(func(opts gpupaas.ListOptions) ([]int, string, error) { n++; return []int{n}, fmt.Sprint(n), nil }); err == nil {
		t.Fatal("an endless list must fail")
	}
}

// A controller that filters after paging can return an empty page that still has a continue marker.
func TestListAllFollowsAnEmptyPageThatHasAMarker(t *testing.T) {
	pages := map[string][]int{"": {1}, "a": nil, "b": {2}}
	next := map[string]string{"": "a", "a": "b", "b": ""}
	got, err := listAll(func(opts gpupaas.ListOptions) ([]int, string, error) {
		return pages[opts.Continue], next[opts.Continue], nil
	})
	if err != nil || len(got) != 2 || got[1] != 2 {
		t.Fatalf("an empty page in the middle must not end the list: %v %v", got, err)
	}
}

func TestListFetchersMapTheFieldsAndRespectTheWorkspace(t *testing.T) {
	ctx := context.Background()
	cs := client.NewFakeClientset()
	pd := &client.ProviderData{Clientset: cs}
	v1 := cs.V1alpha1()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := v1.Workspaces("p").VMInstances("w1").Create(ctx, &apiv1.VMInstance{Metadata: apiv1.ObjectMeta{Name: "vm-b"}, Spec: apiv1.VMInstanceSpec{Type: "vm-sku"}}, gpupaas.CreateOptions{})
	must(err)
	_, err = v1.Workspaces("p").VMInstances("w2").Create(ctx, &apiv1.VMInstance{Metadata: apiv1.ObjectMeta{Name: "vm-a"}, Spec: apiv1.VMInstanceSpec{Type: "vm-sku"}}, gpupaas.CreateOptions{})
	must(err)
	_, err = v1.Workspaces("p").BareMetals("w1").Create(ctx, &apiv1.BareMetal{Metadata: apiv1.ObjectMeta{Name: "bm-1"}, Spec: apiv1.BareMetalSpec{Type: "bm-sku"}}, gpupaas.CreateOptions{})
	must(err)
	_, err = v1.Workspaces("p").Storages("w1").Create(ctx, &apiv1.Storage{Metadata: apiv1.ObjectMeta{Name: "fs"}, Spec: apiv1.StorageSpec{Type: "weka", Size: "50"}}, gpupaas.CreateOptions{})
	must(err)
	_, err = v1.Workspaces("p").SshKeys("w1").Create(ctx, &apiv1.SshKey{Metadata: apiv1.ObjectMeta{Name: "key"}, Spec: apiv1.SshKeySpec{PublicKey: "ssh-ed25519 AAAA"}}, gpupaas.CreateOptions{})
	must(err)

	rows, err := listVMs(ctx, pd, "p", "")
	if err != nil || len(rows) != 2 {
		t.Fatalf("a project list covers its workspaces: %v %v", rows, err)
	}
	byName := map[string]map[string]string{}
	for _, r := range rows {
		byName[r["name"]] = r
	}
	if r := byName["vm-b"]; r["workspace"] != "w1" || r["sku"] != "vm-sku" || r["status"] != "running" {
		t.Errorf("vm-b: %v", r)
	}
	if rows, _ := listVMs(ctx, pd, "p", "w2"); len(rows) != 1 || rows[0]["name"] != "vm-a" {
		t.Errorf("workspace w2: %v", rows)
	}
	if rows, _ := listBareMetals(ctx, pd, "p", ""); len(rows) != 1 || rows[0]["sku"] != "bm-sku" || rows[0]["workspace"] != "w1" {
		t.Errorf("bare metal: %v", rows)
	}
	if rows, _ := listStorageOf(highSpeedStorage)(ctx, pd, "p", ""); len(rows) != 1 || rows[0]["size"] != "50" || rows[0]["sku"] != "weka" || rows[0]["status"] != "success" {
		t.Errorf("storage: %v", rows)
	}
	if rows, _ := listSshKeys(ctx, pd, "p", "w1"); len(rows) != 1 || rows[0]["public_key"] != "ssh-ed25519 AAAA" {
		t.Errorf("ssh keys: %v", rows)
	}
	if rows, _ := listSecurityGroups(ctx, pd, "p", ""); len(rows) != 0 {
		t.Errorf("no groups yet: %v", rows)
	}
	if rows, _ := listClusters(ctx, pd, "p", ""); len(rows) != 0 {
		t.Errorf("no clusters yet: %v", rows)
	}
}

// A list that holds nothing is an empty list, never an error and never null: for_each over it must work.
func TestListFetchersReturnAnEmptySliceWhenThereIsNothing(t *testing.T) {
	pd := &client.ProviderData{Clientset: client.NewFakeClientset()}
	for _, k := range listKinds {
		rows, err := k.fetch(context.Background(), pd, "p", "")
		if err != nil || rows == nil || len(rows) != 0 {
			t.Errorf("%s: rows %v err %v", k.suffix, rows, err)
		}
	}
}
