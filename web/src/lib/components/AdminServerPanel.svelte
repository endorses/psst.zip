<script lang="ts">
  import { page } from "$app/state";
  import { message as m, t } from "$lib/i18n";
  import { selectedAdminSection } from "$lib/admin-section";
  import AdminSectionNav from "./AdminSectionNav.svelte";
  import ServerSettings from "./ServerSettings.svelte";
  import AbuseContactSettings from "./AbuseContactSettings.svelte";
  import ResourcePolicySettings from "./ResourcePolicySettings.svelte";
  import PublicTransferControl from "./PublicTransferControl.svelte";
  import Icon from "./Icon.svelte";

  let { active }: { active: boolean } = $props();
  const sections = [
    { id: "uploads", label: m("adminUploads") },
    { id: "storage", label: m("storage") },
    { id: "access", label: m("adminPublicTransfers") },
    { id: "reports", label: m("adminAbuseReports") },
  ];
  const section = $derived(
    selectedAdminSection(
      page.url,
      sections.map((s) => s.id),
      "uploads",
    ),
  );
  let visited = $state<Record<string, boolean>>({});
  $effect(() => {
    if (active && !visited[section]) visited[section] = true;
  });
</script>

<div class="page-heading">
  <div>
    <h1>{$t(m("serverSettings"))}</h1>
    <p class="muted">{$t(m("adminServerSettingsDescription"))}</p>
  </div>
  <a class="resource-link" href="/?view=resources">
    <Icon name="Overview" size={18} />{$t(m("inspectResourcesAndCleanup"))}
  </a>
</div>
<AdminSectionNav view="server" active={section} items={sections} />
{#each sections as item}
  {#if visited[item.id] || (active && section === item.id)}
    <div class="admin-pane" hidden={section !== item.id}>
      {#if item.id === "uploads"}<ServerSettings />
      {:else if item.id === "storage"}<ResourcePolicySettings />
      {:else if item.id === "access"}
        {#if active && section === "access"}<PublicTransferControl />{/if}
      {:else}<AbuseContactSettings />{/if}
    </div>
  {/if}
{/each}

<style>
  .page-heading {
    display: flex;
    justify-content: space-between;
    align-items: start;
    flex-wrap: wrap;
    gap: 1rem;
  }
  .page-heading p {
    margin-bottom: 0;
  }
  .resource-link {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    min-height: 44px;
    font-size: 0.85rem;
  }
  .admin-pane > :global(section) {
    margin: 0;
    padding: 0;
    border: 0;
  }
  .admin-pane :global(h2:first-child) {
    margin-top: 0;
  }
</style>
