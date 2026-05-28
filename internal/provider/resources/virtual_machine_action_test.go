package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestNormalizeDesiredAction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   types.String
		want string
	}{
		{"null", types.StringNull(), "none"},
		{"unknown", types.StringUnknown(), "none"},
		{"empty", types.StringValue(""), "none"},
		{"start", types.StringValue("start"), "start"},
		{"reboot", types.StringValue("reboot"), "reboot"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeDesiredAction(tc.in)
			if got.ValueString() != tc.want {
				t.Fatalf("normalizeDesiredAction(%v) = %q want %q", tc.in, got.ValueString(), tc.want)
			}
		})
	}
}

func TestNormalizedActionValue(t *testing.T) {
	t.Parallel()
	if normalizedActionValue(types.StringNull()) != "" {
		t.Fatalf("null should normalize to empty string")
	}
	if normalizedActionValue(types.StringUnknown()) != "" {
		t.Fatalf("unknown should normalize to empty string")
	}
	if got := normalizedActionValue(types.StringValue("start")); got != "start" {
		t.Fatalf("got %q want start", got)
	}
}

func TestVMActionDispatchDecision(t *testing.T) {
	t.Parallel()
	// The Update() method only invokes an SDK action when the new action is
	// non-empty, not "none", and differs from the prior state. This test
	// asserts that decision matrix directly so we don't have to spin up a
	// fake VM client.
	cases := []struct {
		name           string
		old, planned   types.String
		shouldDispatch bool
	}{
		{"unset-to-unset", types.StringNull(), types.StringNull(), false},
		{"unset-to-none", types.StringNull(), types.StringValue("none"), false},
		{"none-to-none", types.StringValue("none"), types.StringValue("none"), false},
		{"unset-to-start", types.StringNull(), types.StringValue("start"), true},
		{"none-to-start", types.StringValue("none"), types.StringValue("start"), true},
		{"start-to-start", types.StringValue("start"), types.StringValue("start"), false},
		{"start-to-stop", types.StringValue("start"), types.StringValue("stop"), true},
		{"stop-to-reboot", types.StringValue("stop"), types.StringValue("reboot"), true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			oldA := normalizedActionValue(tc.old)
			newA := normalizedActionValue(tc.planned)
			got := newA != "" && newA != "none" && newA != oldA
			if got != tc.shouldDispatch {
				t.Fatalf("dispatch(old=%q, new=%q) = %v want %v", oldA, newA, got, tc.shouldDispatch)
			}
		})
	}
}

func TestVMActionOptionsFromInputs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics

	if got := vmActionOptionsFromInputs(ctx, nil, &diags); got.Envs != nil || got.Variables != nil {
		t.Fatalf("nil inputs must produce zero ActionOptions, got %+v", got)
	}

	envs, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{"K": "v"})
	vars, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{"VAR": "1"})
	in := &vmActionInputs{Envs: envs, Variables: vars}
	got := vmActionOptionsFromInputs(ctx, in, &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if len(got.Envs) != 1 || got.Envs[0]["K"] != "v" {
		t.Fatalf("envs not propagated, got %+v", got.Envs)
	}
	if len(got.Variables) != 1 || got.Variables[0]["VAR"] != "1" {
		t.Fatalf("variables not propagated, got %+v", got.Variables)
	}
}
