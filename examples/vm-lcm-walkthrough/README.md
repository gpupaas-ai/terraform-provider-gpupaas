# VM LCM Walkthrough — what the user sees

End-to-end lifecycle of a `gpupaas_virtual_machine` with the VMAAS first-class
design (see `plan.md`): async provisioning with true-status waits,
order-agnostic sets, Day-2 restriction (password / security group / sharing),
declarative power state, out-of-band detection, import, and async destroy.
This doc doubles as the acceptance script for the plan's FRs.

## 0. Configuration

See [`main.tf`](./main.tf) in this directory.

Key schema facts:

| Field | Behavior |
|---|---|
| `spec.guest_password` | **Day-2 mutable**, sensitive, write-only (never read back → never false-drifts) |
| `spec.security_group` | **Day-2 mutable** |
| `spec.sharing` | **Day-2 mutable**, fully order-agnostic (sets) |
| `spec.rules` (security group) | order-agnostic set |
| every other `spec.*` + `metadata.*` | **immutable** — editing fails the plan (see §4) |
| `power_state` | declarative `on`/`off`; reconciled against real status |
| `timeouts` | `create`/`update`/`delete` waiter bounds |

## 1. Provision (async LCM)

```console
$ terraform init && terraform apply
Plan: 2 to add, 0 to change, 0 to destroy.

gpupaas_virtual_machine.app: Creating...
gpupaas_virtual_machine.app: Still creating... [10s elapsed]     # waiter polling /status: SUBMITTED
gpupaas_virtual_machine.app: Still creating... [2m40s elapsed]   # PENDING
gpupaas_virtual_machine.app: Creation complete after 4m12s       # status = SUCCESS (true terminal state)
```

The HTTP 200 from create only means SUBMITTED; the provider blocks until the
`/status` sub-resource reports a terminal state **belonging to this
operation** (stale-terminal-state guard, plan §3.4), then re-reads the object
so state holds settled outputs:

```console
$ terraform state show gpupaas_virtual_machine.app | grep private_ip
    status.output.private_ip = "10.0.1.23"
```

Failure mode: `Error: VM provisioning failed: <status.reason>` — apply exits
non-zero, resource is tainted for retry.

## 2. Drift baseline + order-agnosticism

```console
$ terraform plan
No changes. Your infrastructure matches the configuration.
```

Reorder the security-group rules, or list `sharing.projects` in a different
order than the server returns them:

```console
$ terraform plan
No changes.        # sets, not lists — order is semantically irrelevant
```

## 3. Day-2 allowed changes (in-place)

Edit `guest_password`, `security_group`, or `sharing`:

```console
$ terraform apply
  ~ update in-place
  ~ spec.guest_password        = (sensitive value)
  ~ spec.sharing.projects      = [ + { name = "project-c" } ]
gpupaas_virtual_machine.app: Modifying...
gpupaas_virtual_machine.app: Modifications complete after 40s
```

## 4. Day-2 forbidden change → hard error (D4 option (b))

Edit `cpu_count = "8"`:

```console
$ terraform plan
╷
│ Error: cpu_count cannot be modified on an existing VirtualMachine
│   on main.tf line 24, in resource "gpupaas_virtual_machine" "app": spec.cpu_count
│ Day-2 changes are limited to guest_password, security_group, and sharing.
│ To resize, destroy and recreate the VM explicitly.
╵
```

Nothing is called, nothing is destroyed — the plan fails before any API write.
(Backup mode `ImmutableReplace` instead plans `-/+ destroy and then create
replacement  # forces replacement` — a one-line switch, plan §3.3.)

## 5. Power LCM — declarative start/stop + drift

```hcl
power_state = "off"
```

```console
$ terraform apply
  ~ power_state = "on" -> "off"
gpupaas_virtual_machine.app: Modifying...     # POST action/stop, waits for ACTIONCOMPLETE
```

Out-of-band stop (`paasctl vm stop app-vm-1`) while config says `on`:

```console
$ terraform plan
  ~ power_state = "off" -> "on"    # drift detected from /status; apply restarts the VM
```

`reboot` is intentionally not in Terraform (a verb, not a convergeable
state): `paasctl vm reboot app-vm-1 --wait`.

## 6. Out-of-band deletion

Someone runs `paasctl delete vm app-vm-1 -w demo-ws --wait`:

```console
$ terraform plan
  # gpupaas_virtual_machine.app has been deleted outside of Terraform
  + create        # plan proposes re-creating it
```

## 7. Import (adopting existing VMs / remote-state workflows)

```console
$ terraform import gpupaas_virtual_machine.app demo-project/demo-ws/app-vm-1
$ terraform plan
No changes.        # acceptance criterion: import, then plan-clean
```

## 8. Destroy (async, waits for the truth)

```console
$ terraform destroy
gpupaas_virtual_machine.app: Destroying...
gpupaas_virtual_machine.app: Still destroying... [30s elapsed]   # DESTROYPENDING → DESTROYING
gpupaas_virtual_machine.app: Destruction complete after 1m50s    # 404/DESTROYED confirmed
```

On `DESTROYFAILED`: `Error: VM teardown failed: <reason>` and the resource
stays in state for a retry.
