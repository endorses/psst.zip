<script lang="ts">
  import { onMount } from "svelte";
  import { accountRequest } from "$lib/account";
  import { validateAdministratorSecurity } from "$lib/admin-security";
  let enabled = $state<boolean | null>(null),
    unavailable = $state(false),
    disposed = false;
  onMount(() => {
    void accountRequest("/admin/security")
      .then((value) => {
        if (!disposed) enabled = validateAdministratorSecurity(value).enabled;
      })
      .catch(() => {
        if (!disposed) unavailable = true;
      });
    return () => {
      disposed = true;
    };
  });
</script>

{#if enabled === false}<aside class="notice" role="note">
    <strong>Your administrator account has no second factor.</strong> Set up an authenticator and
    save recovery codes to protect server controls.
    <a href="/?view=account">Open account security</a>
  </aside>
{:else if unavailable}<aside class="notice" role="note">
    Administrator security status could not be checked. <a href="/?view=account"
      >Open account security to retry</a
    >.
  </aside>{/if}
