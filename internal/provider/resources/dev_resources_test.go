package resources

import (
	"context"
	"testing"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestSamePublicKeyIgnoresTheComment(t *testing.T) {
	const a = "ssh-ed25519 AAAAC3Nza one@example"
	for name, tc := range map[string]struct {
		b    string
		want bool
	}{
		"identical":           {a, true},
		"comment dropped":     {"ssh-ed25519 AAAAC3Nza", true},
		"comment changed":     {"ssh-ed25519 AAAAC3Nza other@host", true},
		"trailing newline":    {a + "\n", true},
		"different material":  {"ssh-ed25519 AAAAOTHER one@example", false},
		"different key type":  {"ssh-rsa AAAAC3Nza one@example", false},
		"empty from platform": {"", false},
	} {
		if got := samePublicKey(a, tc.b); got != tc.want {
			t.Errorf("%s: samePublicKey = %v, want %v", name, got, tc.want)
		}
	}
}

func cfgRule(cidr, app, port, proto, action string) securityRuleModel {
	return securityRuleModel{
		SourceCIDR: types.StringValue(cidr), Application: types.StringValue(app), ApplicationPort: nullableString(port),
		Protocol: types.StringValue(proto), Action: types.StringValue(action),
	}
}

func TestSecurityGroupStateKeepsConfiguredRulesThePlatformFillsIn(t *testing.T) {
	base := securityGroupResourceModel{
		Metadata: scopedMetadataModel{Name: types.StringValue("web"), Project: types.StringValue("p")},
		Spec: securityGroupSpecModel{Rules: []securityRuleModel{
			cfgRule("0.0.0.0/0", "ssh", "", "tcp", "allow"), // no port configured
			cfgRule("10.0.0.0/8", "https", "443", "tcp", "allow"),
		}},
	}
	srv := &apiv1.SecurityGroup{
		Metadata: apiv1.ObjectMeta{Name: "web"},
		Spec: apiv1.SecurityGroupSpec{Rules: []apiv1.Rule{
			{SourceCIDR: "0.0.0.0/0", Application: "ssh", ApplicationPort: "22", Protocol: "tcp", Action: "allow"}, // the platform filled in 22
			{SourceCIDR: "10.0.0.0/8", Application: "https", ApplicationPort: "443", Protocol: "tcp", Action: "allow"},
		}},
		Status: apiv1.SecurityGroupStatus{Status: "success"},
	}
	var diags diag.Diagnostics
	got := securityGroupModelFromServer(context.Background(), base, srv, &diags)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if !got.Spec.Rules[0].ApplicationPort.IsNull() {
		t.Fatalf("a port the platform filled in must not appear as a change, got %q", got.Spec.Rules[0].ApplicationPort.ValueString())
	}
	if got.Spec.Rules[1].ApplicationPort.ValueString() != "443" || got.ID.ValueString() != "p/web" || got.Status.Status.ValueString() != "success" {
		t.Fatalf("state: %+v", got)
	}
}

func TestSecurityGroupStateShowsDriftAndMissingRules(t *testing.T) {
	base := securityGroupResourceModel{
		Metadata: scopedMetadataModel{Name: types.StringValue("web"), Project: types.StringValue("p"), Workspace: types.StringValue("w")},
		Spec:     securityGroupSpecModel{Rules: []securityRuleModel{cfgRule("0.0.0.0/0", "ssh", "", "tcp", "allow"), cfgRule("10.0.0.0/8", "https", "443", "tcp", "allow")}},
	}
	srv := &apiv1.SecurityGroup{
		Metadata: apiv1.ObjectMeta{Name: "web"},
		Spec: apiv1.SecurityGroupSpec{Rules: []apiv1.Rule{
			{SourceCIDR: "0.0.0.0/0", Application: "ssh", Protocol: "tcp", Action: "deny"}, // changed outside Terraform
			// the second rule was removed outside Terraform
		}},
	}
	var diags diag.Diagnostics
	got := securityGroupModelFromServer(context.Background(), base, srv, &diags)
	if len(got.Spec.Rules) != 1 || got.Spec.Rules[0].Action.ValueString() != "deny" {
		t.Fatalf("drift must show the platform's rules: %+v", got.Spec.Rules)
	}
	if got.ID.ValueString() != "p/w/web" {
		t.Fatalf("id = %q", got.ID.ValueString())
	}
}

func TestSecurityGroupStateTreatsDefaultSharingAsUnset(t *testing.T) {
	base := securityGroupResourceModel{Metadata: scopedMetadataModel{Name: types.StringValue("g"), Project: types.StringValue("p")}}
	srv := &apiv1.SecurityGroup{Metadata: apiv1.ObjectMeta{Name: "g"}, Spec: apiv1.SecurityGroupSpec{Sharing: &apiv1.DevSharingSpec{ShareMode: "None"}}}
	var diags diag.Diagnostics
	if got := securityGroupModelFromServer(context.Background(), base, srv, &diags); got.Spec.Sharing != nil {
		t.Fatalf("the platform's default sharing must not show as a change: %+v", got.Spec.Sharing)
	}
	// a sharing block the user did set is kept as the platform returns it
	base.Spec.Sharing = &SharingModel{ShareMode: types.StringValue("ALL_WORKSPACES")}
	srv.Spec.Sharing = &apiv1.DevSharingSpec{ShareMode: "ALL_WORKSPACES"}
	if got := securityGroupModelFromServer(context.Background(), base, srv, &diags); got.Spec.Sharing == nil || got.Spec.Sharing.ShareMode.ValueString() != "ALL_WORKSPACES" {
		t.Fatalf("configured sharing lost: %+v", got.Spec.Sharing)
	}
}

func TestSshKeyStateKeepsTheConfiguredKeyTextWhenOnlyTheCommentDiffers(t *testing.T) {
	base := sshKeyResourceModel{
		Metadata: scopedMetadataModel{Name: types.StringValue("k"), Project: types.StringValue("p")},
		Spec:     sshKeySpecModel{PublicKey: types.StringValue("ssh-ed25519 AAAA me@laptop")},
	}
	var diags diag.Diagnostics
	got := sshKeyModelFromServer(context.Background(), base, &apiv1.SshKey{Metadata: apiv1.ObjectMeta{Name: "k"}, Spec: apiv1.SshKeySpec{PublicKey: "ssh-ed25519 AAAA"}}, &diags)
	if got.Spec.PublicKey.ValueString() != "ssh-ed25519 AAAA me@laptop" {
		t.Fatalf("a dropped comment must not look like drift, got %q", got.Spec.PublicKey.ValueString())
	}
	got = sshKeyModelFromServer(context.Background(), base, &apiv1.SshKey{Metadata: apiv1.ObjectMeta{Name: "k"}, Spec: apiv1.SshKeySpec{PublicKey: "ssh-ed25519 BBBB"}}, &diags)
	if got.Spec.PublicKey.ValueString() != "ssh-ed25519 BBBB" {
		t.Fatalf("a different key must show as drift, got %q", got.Spec.PublicKey.ValueString())
	}
}
