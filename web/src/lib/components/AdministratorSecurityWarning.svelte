<script lang="ts">
  import { message as m, t } from "$lib/i18n";

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
    <strong>{$t(m("yourAdministratorAccountHasNoSecondFactor"))}</strong>
    {$t(m("setUpAnAuthenticatorAndSaveRecoveryCodesTo"))}
    <a href="/?view=account">{$t(m("openAccountSecurity"))}</a>
  </aside>
{:else if unavailable}<aside class="notice" role="note">
    {$t(m("administratorSecurityStatusCouldNotBeChecked"))}
    <a href="/?view=account">{$t(m("openAccountSecurityToRetry"))}</a>.
  </aside>{/if}
