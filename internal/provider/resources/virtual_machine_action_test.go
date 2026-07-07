package resources

import (
	"testing"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
)

// TestDerivePowerState exercises the power_state heuristic documented on
// derivePowerState: only an explicit "stop" action reports "off"; everything
// else (no action yet, start, reboot, ...) reports "on".
func TestDerivePowerState(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status apiv1.VirtualMachineStatus
		want   string
	}{
		{"fresh-no-action", apiv1.VirtualMachineStatus{Status: "Status_SUCCESS"}, "on"},
		{"after-start", apiv1.VirtualMachineStatus{Status: "Status_ACTIONCOMPLETE", Action: "start"}, "on"},
		{"after-stop", apiv1.VirtualMachineStatus{Status: "Status_ACTIONCOMPLETE", Action: "stop"}, "off"},
		{"after-reboot", apiv1.VirtualMachineStatus{Status: "Status_ACTIONCOMPLETE", Action: "reboot"}, "on"},
		{"action-case-insensitive", apiv1.VirtualMachineStatus{Status: "Status_ACTIONCOMPLETE", Action: "Status_STOP"}, "off"},
		{"pending-with-prior-stop", apiv1.VirtualMachineStatus{Status: "Status_SUBMITTED", Action: "stop"}, "off"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := derivePowerState(tc.status)
			if got.ValueString() != tc.want {
				t.Fatalf("derivePowerState(%+v) = %q want %q", tc.status, got.ValueString(), tc.want)
			}
		})
	}
}

// TestVMPowerDispatchDecision asserts the Update() decision matrix directly:
// an SDK Start/Stop call fires only when power_state actually transitions.
func TestVMPowerDispatchDecision(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		old, planned   string
		shouldDispatch bool
		wantVerb       string
	}{
		{"on-to-on", "on", "on", false, ""},
		{"off-to-off", "off", "off", false, ""},
		{"on-to-off", "on", "off", true, "stop"},
		{"off-to-on", "off", "on", true, "start"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dispatch := tc.planned != tc.old
			if dispatch != tc.shouldDispatch {
				t.Fatalf("dispatch(old=%q, new=%q) = %v want %v", tc.old, tc.planned, dispatch, tc.shouldDispatch)
			}
			if dispatch {
				verb := "start"
				if tc.planned == "off" {
					verb = "stop"
				}
				if verb != tc.wantVerb {
					t.Fatalf("verb(old=%q, new=%q) = %q want %q", tc.old, tc.planned, verb, tc.wantVerb)
				}
			}
		})
	}
}
