# Manual production deployment through restricted SSH

`.github/workflows/deploy.yml` adds a manual **Deploy published container release**
workflow on `main`. It sends `update vMAJOR.MINOR.PATCH` to the installed
root-owned updater. The host authenticates the published ready manifest, bundle,
image indexes and architecture children using its fixed verification policy.
The workflow does not build or publish an image. Commits and tags do not trigger
this production workflow.

The main-only production environment and restricted deployment identity are
configured and tested on the VPS. The installed `runtime-v1` profile activates
normal updates after bounded deployment checks, with no administrator credentials
or manual flow prompts. Functional behavior remains covered by CI.

The first end-to-end [Actions deployment](https://github.com/endorses/psst.zip/actions/runs/37928906539)
reapplied the active immutable v0.1.6 release successfully on 2026-10-09. The job
took **70 seconds**, including a **65-second SSH deployment request**. Public
trusted HTTPS, release identity, original ciphertext and all physical volumes
were independently verified afterward; retention completed. This is a measured
same-version rehearsal, not a duration guarantee for larger future releases.
The [release update guide](release-update-recovery.md) describes host configuration
and recovery. Off-host backups remain explicitly deferred; checkpoints currently
protect against update failures on the same VPS.

## Active environment policy

On 2026-10-08, the `production` environment was created as ID `23741939516`.
Its only deployment policy is branch `main`, ID `62320707`, with no tag policy or
required reviewer. Manual dispatch remains the deliberate operator action for
this personal instance. The branch itself requires a PR and all five existing
GitHub Actions checks, with force pushes and deletion blocked.

Separate API reads verified the environment settings and the single allowed
branch. On 2026-10-09, the environment received `VPS_HOST`, `DEPLOY_SSH_KEY` and
`VPS_KNOWN_HOSTS`. The key is dedicated to deployment; the maintenance key and
application credentials are absent from Actions. The pinned Ed25519 host key was
obtained through the authenticated maintenance connection. Its fingerprint is
`SHA256:ghxhCIu6hTxBzsObWqDvv8MyNwxLf61xnynwNgZgjZU`.

The installed OpenSSH configuration and its actual Include ordering passed
validation. Effective settings prohibit password authentication, arbitrary commands,
TTY, forwarding and tunnels. The account has no Docker group or general sudo
access. Actual connections accepted `status` and rejected eight forbidden command
cases, SFTP, PTY allocation and forwarding. A fresh maintenance connection also
passed, with unchanged effective policy. The successful Actions deployment used
this same restricted identity and the reviewed helper installed from PR #34.

The environment request payloads are tracked in
[production.json](../../.github/environments/production.json) and
[production-branch-policy.json](../../.github/environments/production-branch-policy.json).
They can be inspected before applying the same settings to another installation.
[GitHub environment API](https://docs.github.com/en/rest/deployments/environments#create-or-update-an-environment),
[deployment branch-policy API](https://docs.github.com/en/rest/deployments/branch-policies#create-a-deployment-branch-policy).

## Configure the host after release and recovery verification

Keep the personal maintenance identity for interactive administration and
recovery. Create a separate `psst-deploy` account with a normal shell for sshd's
forced-command execution, no password authentication, no Docker group membership
and no other sudo grants. These instructions target a Debian/Ubuntu OpenSSH host;
check effective configuration on the selected host before enabling the identity.

Install the already reviewed updater and parser as described in the update guide.
An application release never updates these privileged installed files itself.
Their entire parent tree must remain root-owned and protected against other
users' writes. Provision and test `/etc/psst.zip/deployment.json` with actual
storage and protected hooks before allowing a deployment request.

Create a separate deployment Ed25519 key on the operator's machine. The Actions
client currently requires an OpenSSH private key usable without an interactive
passphrase. Store its private half only in the deployment secret and protected
operator recovery storage. The maintenance key does not belong in Actions.
For example, choose a dedicated filename with `ssh-keygen -t ed25519`; transfer
only its `.pub` file to the VPS through the existing maintenance connection.

As administrator, install the fixed account's public authorization outside its
writable home. Replace the example source path with the dedicated public file:

```sh
install -d -o root -g root -m 0755 /etc/ssh/psst-deploy
install -o root -g root -m 0644 /private/operator/psst-deploy.pub \
  /etc/ssh/psst-deploy/authorized_keys
visudo -c -f deploy/ssh/psst-deploy.sudoers
install -o root -g root -m 0440 deploy/ssh/psst-deploy.sudoers \
  /etc/sudoers.d/psst-deploy
install -o root -g root -m 0644 deploy/ssh/psst-deploy.conf \
  /etc/ssh/sshd_config.d/70-psst-deploy.conf
```

The root-owned public key file can be readable: it contains only public keys.
The Match configuration forces the exact sudo/Python isolated-mode command,
disables forwarding/tunnels/TTY and allows public-key authentication only.
The sudo rule allows that exact `ssh` entry point and preserves only the original
SSH command needed by its strict parser. It grants no local verification,
restore, arbitrary Python script, shell or Docker command.

Keep the maintenance connection open. Validate both full sshd configuration and
the effective deployment user's Match settings before reloading the SSH service:

```sh
sshd -t
sshd -T -C user=psst-deploy,host=YOUR_VPS_HOST,addr=YOUR_CLIENT_IP
sudo -l -U psst-deploy
systemctl reload ssh
```

Use actual host/client values. Confirm the effective forced command,
`authorizedkeysfile`, disabled password/keyboard authentication, disabled
forwarding and TTY. Also confirm the maintenance account's effective settings
and test a new maintenance connection before closing the old one. A local
template parse does not verify another host's Includes, PAM account policy or
conflicting earlier Match rules.

With the dedicated key and an independently verified pinned host key, test
`ssh psst-deploy@YOUR_VPS_HOST status`. Attempts to execute `id`, a shell, SFTP,
`verify`, `restore`, extra arguments or shell operators must fail. Port forwarding
and TTY requests must fail. A malformed update version must fail before any
transaction starts. Run the first real update only after a published ready release
and recoverable checkpoint have been verified. The production installation can
select `verification_profile: runtime-v1` in its root-owned configuration to
activate normal updates automatically after actual HTTPS/health/version/settings/
storage and SQLite checks. This mode requires no administrator credentials or
fixture accounts; functional behavior remains covered by CI.

## Configure GitHub

Create the repository's `production` environment and allow deployment only from
the protected `main` branch. Protect `main` against unreviewed changes to workflows
or deployment code. Optional required reviewers add another gate; manual dispatch
is the normal personal-instance release selection. Neither a pull request nor a
tagged workflow copy can access this environment under these settings.

| Environment setting      | Contents                                                                    |
| ------------------------ | --------------------------------------------------------------------------- |
| Variable `VPS_HOST`      | The exact VPS IPv4/IPv6 address or DNS hostname, without a port or username |
| Secret `DEPLOY_SSH_KEY`  | The separate deployment OpenSSH private key                                 |
| Secret `VPS_KNOWN_HOSTS` | One or more independently verified host key lines naming exactly `VPS_HOST` |

The SSH user is fixed as `psst-deploy`, and the port is fixed at 22. Existing SSH
firewall access is sufficient; this workflow needs no new port or inbound service.
Public HTTPS access remains governed by the existing Caddy/firewall setup.

Obtain the host public key through an authenticated maintenance connection or
provider console, and compare its fingerprint through that trusted path. Record
each line as `EXACT_VPS_HOST ssh-ed25519 BASE64_PUBLIC_HOST_KEY` (or another
supported full public host key type). Do not establish trust from `ssh-keyscan`
inside Actions. Hashed names, wildcard/alias records and CA markers are outside
this client's deliberately limited pinned-key format. Multiple verified keys for
the same exact host support controlled rotation.

The client disables user SSH configuration, agent identities, global known_hosts,
automatic host-key updates and forwarding. It writes the key and pinned hosts to
a private temporary directory, validates the key noninteractively and removes the
files on normal completion or handled failure. Neither secret appears in command
arguments, uploaded artifacts or request logs. Child processes receive a minimal
environment rather than the workflow's secret-bearing environment.

## Dispatch and observe

Select the workflow on `main` and enter a published ready version such as
`v1.2.3`. The client accepts only strict numeric stable versions; it cannot pass
an image URL, commit, path, branch, bootstrap credential or activation override.
The host independently constrains the trusted release repository and images.

Per-production workflow concurrency queues requests without canceling a running
deployment. The host lock also prevents concurrent updates from other SSH sessions.
A successful request requires both SSH exit zero and the helper's protected
response confirming the requested version as active with phase `completed`.
The SSH client caps a deployment request at ten minutes and bounds its output.
Image acquisition and checkpointing still take time proportional to the selected
images and existing data; runtime validation and activation share a 120-second
budget.

In the default `full` profile, an exit of 20 means the isolated candidate is awaiting the local administrator's
verification. Actions reports failure/pending work, rather than declaring
production successfully deployed; use the maintenance identity to complete that
gate. `runtime-v1` performs normal activation without that interactive step;
full-flow unattended activation instead requires an installed protected hook.
Restores always retain full reconciliation and verification.

If the connection times out, is interrupted or reports failure, inspect the
durable host transaction with the maintenance identity. Do not assume rollback,
blindly retry a new version or run the old binary on potentially migrated data.
Follow the update guide's explicit recovery path. The client bounds output and
suppresses raw remote errors; protected host state is the diagnostic source.

## Rotation and remaining verification

To revoke deployment access, remove the dedicated public key from the root-owned
authorization file and remove its GitHub secret. For rotation, provision and test
the new dedicated key before removing the old key, then update the environment
secret. Verify changed host keys through the independent trusted path before
editing `VPS_KNOWN_HOSTS`. Keep maintenance/recovery credentials separate.

- [x] Validate the effective installed SSH/sudo policy and denial cases on the VPS.
- [x] Configure and verify branch/environment restrictions and dedicated secrets.
- [x] Exercise a selected published ready release through the installed helper.
      Profile/report rejection, timeout and failure recovery have focused automated
      coverage; no failure was deliberately injected into the live production site.
- [x] Run a same-version production update through manual Actions dispatch and
      confirm durable automatic completion and persisted application state.

Transaction `20261009T121612Z-9393b2aeb816` completed at
`2026-10-09T12:17:15.237859+00:00`, with `active_version: v0.1.6`,
`verification_profile: runtime-v1`, `checkpoint_protection: local-only` and
`cleanup.status: completed`. Its report contains exactly the five runtime checks;
it makes no authenticated transfer or native decryption claim. The retained
15,355-byte payload matched the original cold checkpoint byte-for-byte. Both
services retained `unless-stopped`, the backend published no host port, and Caddy
published 80/443. After checkpointing and retention, 34,146,754,560 bytes were free
on the 40 GB disk.
