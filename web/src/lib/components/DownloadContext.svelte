<script lang="ts">
  import type { Snippet } from "svelte";
  import type { User } from "$lib/account";
  import Icon from "./Icon.svelte";
  let {
    user,
    inboxId,
    returnUrl,
    onlogout,
    signingOut = false,
    children,
  }: {
    user: User | null;
    inboxId: string;
    returnUrl: string;
    onlogout: () => Promise<void>;
    signingOut?: boolean;
    children: Snippet;
  } = $props();
  const items = ["Send", "Receive", "Scan", "History", "Settings"] as const;
</script>

{#if user?.role === "user" && !user.must_change_password && inboxId}
  <div class="owner-workspace">
    <aside>
      <p class="sidebar-label">Your workspace</p>
      <nav aria-label="Account navigation">
        {#each items as item}
          <a
            href={item === "Receive" ? returnUrl : `/?view=${item.toLowerCase()}`}
            aria-label={item === "Scan" ? "Scan QR code" : item}
            aria-current={item === "Receive" ? "location" : undefined}
            class:active={item === "Receive"}
          >
            <Icon name={item === "Scan" ? "QRCode" : item} /><span>{item}</span>
          </a>
        {/each}
      </nav>
      <section class="account-card" aria-label="Signed-in account">
        <a class="identity" href="/?view=account"
          ><span class="avatar" aria-hidden="true">{user.username.slice(0, 1).toUpperCase()}</span
          ><span><span class="muted small">Signed in as</span><strong>{user.username}</strong></span
          ></a
        >
        <button onclick={onlogout} disabled={signingOut}
          ><Icon name="SignOut" size={17} />Sign out</button
        >
      </section>
    </aside>
    <div class="owner-content">
      <a class="back-link" href={returnUrl}>← Back to received files</a>{@render children()}
    </div>
  </div>
{:else}
  {@render children()}
{/if}

<style>
  .owner-workspace {
    display: grid;
    grid-template-columns: 200px minmax(0, 1fr);
    gap: clamp(2rem, 5vw, 4.5rem);
    align-items: start;
  }
  aside {
    position: sticky;
    top: 2rem;
  }
  .sidebar-label {
    font-size: 0.7rem;
    font-weight: 650;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    color: var(--muted);
    margin: 0.2rem 0.8rem 1rem;
  }
  nav {
    display: grid;
    gap: 0.35rem;
  }
  nav a {
    min-height: 48px;
    display: flex;
    gap: 0.8rem;
    align-items: center;
    padding: 0.65rem 0.85rem;
    border-radius: 10px;
    color: var(--muted);
    text-decoration: none;
    font-weight: 550;
  }
  nav a:hover {
    background: var(--hover);
    color: var(--text);
  }
  nav a.active {
    background: var(--accent);
    color: var(--primary);
    box-shadow: inset 3px 0 var(--primary);
  }
  .account-card {
    margin-top: 2rem;
    border: 1px solid var(--divider);
    border-radius: 14px;
    background: var(--surface);
  }
  .identity {
    display: flex;
    align-items: center;
    gap: 0.65rem;
    padding: 0.9rem;
    color: var(--text);
    text-decoration: none;
    overflow-wrap: anywhere;
  }
  .identity > span:last-child {
    display: grid;
    min-width: 0;
  }
  .avatar {
    flex: none;
    width: 34px;
    height: 34px;
    display: grid;
    place-items: center;
    border-radius: 50%;
    color: var(--primary);
    background: var(--accent);
  }
  .account-card button {
    width: 100%;
    justify-content: flex-start;
    border: 0;
    border-top: 1px solid var(--divider);
    border-radius: 0 0 13px 13px;
    padding: 0.6rem 0.9rem;
    color: var(--muted);
    background: var(--elevated);
  }
  .owner-content {
    min-width: 0;
  }
  .back-link {
    display: inline-flex;
    align-items: center;
    min-height: 44px;
    margin-bottom: 1rem;
    text-decoration: none;
    font-size: 0.85rem;
  }
  @media (max-width: 850px) {
    .owner-workspace {
      display: block;
    }
    aside {
      position: static;
      display: flex;
      flex-direction: column-reverse;
      margin-bottom: 2rem;
    }
    .sidebar-label,
    .avatar {
      display: none;
    }
    nav {
      grid-template-columns: repeat(5, minmax(0, 1fr));
      border-bottom: 1px solid var(--divider);
      gap: 0;
    }
    nav a {
      justify-content: center;
      gap: 0.35rem;
      padding: 0.75rem 0.25rem;
      border-radius: 0;
      font-size: 0.85rem;
    }
    nav a.active {
      background: transparent;
      box-shadow: inset 0 -3px var(--primary);
    }
    .account-card {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin: 0 0 1rem;
    }
    .account-card button {
      width: auto;
      border: 0;
      background: transparent;
      margin: 0.4rem;
      border-radius: 10px;
    }
    .identity {
      padding: 0.75rem;
    }
  }
  @media (max-width: 600px) {
    nav a {
      flex-direction: column;
      min-height: 72px;
      font-size: 0.72rem;
      gap: 0.5rem;
      padding: 0.7rem 0.1rem;
    }
    nav span {
      white-space: nowrap;
    }
  }
</style>
