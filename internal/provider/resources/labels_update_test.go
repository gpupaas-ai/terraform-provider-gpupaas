package resources

import (
	"context"
	"testing"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// An update sends the platform's own labels back: configured labels are merged into them, and only a key the user
// removed from the configuration is dropped.
func TestMergeLabelsKeepsThePlatformsLabels(t *testing.T) {
	server := map[string]string{"paas.envmgmt.io/compute_type": "baremetal", "env": "old", "gone": "x", "console": "y"}
	got := mergeLabels(server, mapFromStringMap(map[string]string{"env": "new", "added": "1"}), mapFromStringMap(map[string]string{"env": "old", "gone": "x"}))
	want := map[string]string{"paas.envmgmt.io/compute_type": "baremetal", "env": "new", "added": "1", "console": "y"}
	if !sameStringMap(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	if _, ok := server["env"]; !ok || server["env"] != "old" {
		t.Fatal("the platform's map was modified in place")
	}
}

// A display name or description that did not change is not sent again; a changed one is.
func TestApplyUpdateMetadataSendsOnlyChangedNames(t *testing.T) {
	current := apiv1.ObjectMeta{DisplayName: "", Description: "", Labels: map[string]string{"system": "x"}}
	prior := MetadataModel{DisplayName: types.StringValue("Nice"), Description: types.StringValue("same"), Labels: types.MapNull(types.StringType)}

	meta := apiv1.ObjectMeta{DisplayName: "Nice", Description: "same"}
	applyUpdateMetadata(&meta, current, prior, prior)
	if meta.DisplayName != "" || meta.Description != "" {
		t.Fatalf("unchanged names were sent again: %+v", meta)
	}

	changed := prior
	changed.Description = types.StringValue("edited")
	meta = apiv1.ObjectMeta{DisplayName: "Nice", Description: "edited"}
	applyUpdateMetadata(&meta, current, changed, prior)
	if meta.Description != "edited" {
		t.Fatalf("a changed description was dropped: %+v", meta)
	}
}

// Labels that were not configured are left to the platform: nothing is replaced.
func TestApplyUpdateMetadataLeavesUnconfiguredLabelsAlone(t *testing.T) {
	current := apiv1.ObjectMeta{Labels: map[string]string{"system": "x"}}
	plan := MetadataModel{Labels: types.MapNull(types.StringType)}
	meta := apiv1.ObjectMeta{Labels: map[string]string{"system": "x"}}
	applyUpdateMetadata(&meta, current, plan, plan)
	if !sameStringMap(meta.Labels, map[string]string{"system": "x"}) {
		t.Fatalf("labels = %v", meta.Labels)
	}
}

// One drifted tag must not pull the platform's other tags into the state: they would count as managed, and the next
// apply would delete them.
func TestKeepOrDriftLabelsReportsOnlyConfiguredKeysOnDrift(t *testing.T) {
	prior := mapFromStringMap(map[string]string{"a": "1", "b": "2"})
	server := map[string]string{"a": "changed", "b": "2", "console": "z", "platform": "p"}
	got := elementsToStringMap(keepOrDriftLabels(prior, server, false))
	if !sameStringMap(got, map[string]string{"a": "changed", "b": "2"}) {
		t.Fatalf("state tags = %v, want only the configured keys with the server's values", got)
	}
	// A configured key the platform lost is left out (that is the drift), the others stay.
	got = elementsToStringMap(keepOrDriftLabels(prior, map[string]string{"b": "2", "console": "z"}, false))
	if !sameStringMap(got, map[string]string{"b": "2"}) {
		t.Fatalf("state tags = %v", got)
	}
	// No drift: the prior value is kept as is, whatever else the platform holds.
	if kept := keepOrDriftLabels(prior, map[string]string{"a": "1", "b": "2", "console": "z"}, false); !kept.Equal(prior) {
		t.Fatalf("an undrifted map changed: %v", kept)
	}
}

func rule(cidr, app, port string) securityRuleModel {
	return securityRuleModel{SourceCIDR: str(cidr), Application: str(app), ApplicationPort: nullableString(port), Protocol: str("tcp"), Action: str("allow")}
}

// The same rules in another order are not a change; a rule added, removed or changed is.
func TestSameRulesAnyOrder(t *testing.T) {
	ssh, https := rule("0.0.0.0/0", "ssh", ""), rule("10.0.0.0/8", "https", "443")
	if !sameRulesAnyOrder([]securityRuleModel{ssh, https}, []securityRuleModel{https, ssh}) {
		t.Fatal("a reorder was treated as a change")
	}
	if sameRulesAnyOrder([]securityRuleModel{ssh, https}, []securityRuleModel{ssh}) {
		t.Fatal("a removed rule was treated as the same")
	}
	if sameRulesAnyOrder([]securityRuleModel{ssh, ssh}, []securityRuleModel{ssh, https}) {
		t.Fatal("a duplicate must not stand in for a different rule")
	}
	changed := https
	changed.Action = str("deny")
	if sameRulesAnyOrder([]securityRuleModel{ssh, https}, []securityRuleModel{ssh, changed}) {
		t.Fatal("a changed rule was treated as the same")
	}
}

// Read keeps the configured order when the platform holds the same rules, so the plan after a reorder is empty.
func TestSecurityGroupReadKeepsTheConfiguredOrder(t *testing.T) {
	ssh, https := rule("0.0.0.0/0", "ssh", ""), rule("10.0.0.0/8", "https", "443")
	srv := &apiv1.SecurityGroup{Metadata: apiv1.ObjectMeta{Name: "web"}, Spec: apiv1.SecurityGroupSpec{Rules: []apiv1.Rule{
		{SourceCIDR: "0.0.0.0/0", Application: "ssh", Protocol: "tcp", Action: "allow"},
		{SourceCIDR: "10.0.0.0/8", Application: "https", ApplicationPort: "443", Protocol: "tcp", Action: "allow"},
	}}}
	base := securityGroupResourceModel{Spec: securityGroupSpecModel{Rules: []securityRuleModel{https, ssh}}}
	var d diag.Diagnostics
	got := securityGroupModelFromServer(context.Background(), base, srv, &d)
	if len(got.Spec.Rules) != 2 || got.Spec.Rules[0].Application.ValueString() != "https" {
		t.Fatalf("the configured order was lost: %+v", got.Spec.Rules)
	}
}
