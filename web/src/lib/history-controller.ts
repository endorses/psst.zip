import { message as m, LocalizedError } from "./i18n/index.ts";
import { HISTORY_CHANNEL } from "./history-notifications.ts";
import { AccountError } from "./account.ts";
import { loadResourcePage, type ResourcePage } from "./resource-history.ts";
import {
  HistoryCacheCorruption,
  resetHistoryCache,
  cacheHistoryChanges,
  cacheHistorySnapshot,
  cachedHistoryPage,
  cachedHistoryState,
  serverHistoryCursor,
  type HistoryScope,
  type HistorySyncState,
} from "./history-cache.ts";
import {
  historyRetryDelay,
  historySyncSupported,
  loadHistoryChanges,
  HISTORY_SYNC_BATCHES,
  HISTORY_SYNC_INTERVAL,
  HISTORY_RESPONSE_BYTES,
  type HistoryFilter,
} from "./history-sync.ts";

export interface HistoryControllerOptions {
  scope: HistoryScope;
  page: (page: ResourcePage) => void | Promise<void>;
  error: (cause: unknown) => void;
  busy: (value: boolean) => void;
}
/** Owns History requests only, never account/security or payload requests. */
export class HistoryController {
  private active = false;
  private visible = true;
  private epoch = 0;
  private filter: HistoryFilter;
  private cursor = "";
  private supported?: boolean;
  private failures = 0;
  private retryNotBefore = 0;
  private request?: AbortController;
  private running?: Promise<boolean>;
  private timer?: ReturnType<typeof setTimeout>;
  private pending = false;
  private channel?: BroadcastChannel;
  private readonly options: HistoryControllerOptions;
  constructor(options: HistoryControllerOptions) {
    this.options = options;
    if (typeof BroadcastChannel !== "undefined") {
      this.channel = new BroadcastChannel(HISTORY_CHANNEL);
      this.channel.onmessage = (event) => {
        if (event.data?.mutation === true) {
          this.mutation();
          return;
        }
        if (event.data === this.options.scope && this.active && this.visible && !this.running)
          void this.render(this.epoch).catch(() => {});
      };
    }
  }
  private current(epoch: number) {
    return this.active && this.visible && epoch === this.epoch;
  }
  private schedule(delay: number) {
    clearTimeout(this.timer);
    if (!this.active || !this.visible) return;
    const deadline = Date.now() + delay;
    const tick = () => {
      if (!this.active || !this.visible) return;
      const remaining = deadline - Date.now();
      if (remaining <= 0) {
        void this.refresh();
        return;
      }
      this.timer = setTimeout(tick, Math.min(2147483647, remaining));
    };
    this.timer = setTimeout(tick, Math.min(2147483647, Math.max(0, delay)));
  }
  private async render(epoch: number): Promise<boolean> {
    const page = await cachedHistoryPage(
      this.options.scope,
      this.filter,
      this.cursor,
      this.request?.signal,
    );
    if (!this.current(epoch) || !page) return false;
    await this.options.page(page);
    return true;
  }
  async enter(filter: HistoryFilter, cursor: string): Promise<boolean> {
    this.stop();
    this.active = true;
    this.filter = filter;
    this.cursor = cursor;
    const epoch = this.epoch;
    // Cache reading precedes capability discovery and any HTTP history request.
    await this.render(epoch).catch(async (error) => {
      if (error instanceof HistoryCacheCorruption && this.current(epoch))
        await resetHistoryCache(this.options.scope).catch(() => {});
      return false;
    });
    if (!this.current(epoch)) return true;
    return this.refresh();
  }
  async refresh(): Promise<boolean> {
    if (!this.active || !this.visible) return true;
    if (Date.now() < this.retryNotBefore) {
      this.schedule(this.retryNotBefore - Date.now());
      return true;
    }
    if (this.running) {
      this.pending = true;
      return this.running;
    }
    const epoch = this.epoch,
      request = new AbortController();
    this.request = request;
    clearTimeout(this.timer);
    this.options.busy(true);
    let retry = HISTORY_SYNC_INTERVAL;
    let storageFailed = false;
    let failed = false;
    const work = async () => {
      try {
        this.supported ??= await historySyncSupported(request.signal);
        let state: HistorySyncState | undefined;
        try {
          state = await cachedHistoryState(this.options.scope, request.signal);
        } catch (error) {
          if (error instanceof HistoryCacheCorruption) {
            await resetHistoryCache(this.options.scope, request.signal);
          } else {
            // Storage denial still allows a bounded read-only online page. Never
            // claim a saved checkpoint or retry file payloads to repair storage.
            const page = await loadResourcePage(
              serverHistoryCursor(this.cursor, this.filter),
              false,
              request.signal,
              this.filter,
              HISTORY_RESPONSE_BYTES,
            );
            if (this.current(epoch)) await this.options.page(page);
            this.failures = 0;
            this.retryNotBefore = 0;
            return true;
          }
        }
        const snapshot = async (
          filter: HistoryFilter,
          cursor: string,
          reset = false,
          cacheCursor = cursor,
        ) => {
          const page = await loadResourcePage(
            cursor,
            false,
            request.signal,
            filter,
            HISTORY_RESPONSE_BYTES,
          );
          if (!this.current(epoch)) return;
          if (this.supported && (!page.sync_cursor || !page.generation))
            throw new LocalizedError(m("thisServerReturnedAnUnsupportedResourcePageRefreshOr"));
          try {
            await cacheHistorySnapshot(
              this.options.scope,
              filter,
              cacheCursor,
              page,
              state,
              reset,
              request.signal,
            );
            state = await cachedHistoryState(this.options.scope, request.signal);
          } catch (error) {
            if (!(error instanceof HistoryCacheCorruption) && !request.signal.aborted)
              storageFailed = true;
            throw error;
          }
        };
        let snapshotChanged = false;
        if (this.supported && !state) {
          await snapshot(undefined, "");
          snapshotChanged = true;
        }
        let page = await cachedHistoryPage(
          this.options.scope,
          this.filter,
          this.cursor,
          request.signal,
        );
        if (!page || page.stale || !this.supported) {
          snapshotChanged = true;
          await snapshot(
            this.filter,
            serverHistoryCursor(this.cursor, this.filter),
            false,
            this.cursor,
          );
          page = await cachedHistoryPage(
            this.options.scope,
            this.filter,
            this.cursor,
            request.signal,
          );
        }
        let rendered = false;
        if (snapshotChanged && page && this.current(epoch)) {
          await this.options.page(page);
          rendered = true;
        }
        if (this.supported && state) {
          try {
            for (let index = 0; index < HISTORY_SYNC_BATCHES && this.current(epoch); index++) {
              const batch = await loadHistoryChanges(
                state.cursor,
                state.generation,
                request.signal,
              );
              let applied: boolean;
              try {
                applied = await cacheHistoryChanges(
                  this.options.scope,
                  state,
                  batch,
                  request.signal,
                );
              } catch (error) {
                if (!(error instanceof HistoryCacheCorruption) && !request.signal.aborted)
                  storageFailed = true;
                throw error;
              }
              if (!this.current(epoch)) return true;
              if (!applied) {
                state = await cachedHistoryState(this.options.scope, request.signal);
                break;
              }
              state = { ...state, cursor: batch.next_cursor };
              if (batch.changes.length) {
                await this.render(epoch);
                rendered = true;
                this.channel?.postMessage(this.options.scope);
              }
              if (!batch.has_more) break;
              // Yield to rendering; four batches is a hard per-cycle work cap.
              await new Promise((resolve) => setTimeout(resolve, 0));
            }
          } catch (error) {
            if (
              !(
                error instanceof AccountError &&
                error.status === 409 &&
                error.code === "history_sync_reset_required"
              )
            )
              throw error;
            await snapshot(undefined, "", true);
            if (this.filter || this.cursor)
              await snapshot(
                this.filter,
                serverHistoryCursor(this.cursor, this.filter),
                false,
                this.cursor,
              );
            await this.render(epoch);
            rendered = true;
          }
        }
        if (this.current(epoch)) {
          // An empty successful feed still recovers a previous transport error.
          // Reuse the bounded cache page once to clear its stale UI warning.
          if (this.failures > 0 && !rendered) await this.render(epoch);
          if (!this.current(epoch)) return true;
          this.failures = 0;
          this.retryNotBefore = 0;
        }
        return true;
      } catch (error) {
        if (!this.current(epoch) || request.signal.aborted) return true;
        let recovered = false;
        if (error instanceof HistoryCacheCorruption) {
          try {
            await resetHistoryCache(this.options.scope, request.signal);
            this.pending = true;
            recovered = true;
          } catch {}
        } else if (storageFailed) {
          try {
            const page = await loadResourcePage(
              serverHistoryCursor(this.cursor, this.filter),
              false,
              request.signal,
              this.filter,
              HISTORY_RESPONSE_BYTES,
            );
            if (this.current(epoch)) await this.options.page(page);
            this.failures = 0;
            this.retryNotBefore = 0;
            return true;
          } catch (fallbackError) {
            error = fallbackError;
          }
        }
        failed = true;
        retry = recovered
          ? 0
          : historyRetryDelay(
              ++this.failures,
              error instanceof AccountError ? error.retryAfterMs : undefined,
            );
        if (error instanceof AccountError && error.retryAfterMs)
          this.retryNotBefore = Math.max(this.retryNotBefore, Date.now() + error.retryAfterMs);
        this.options.error(error);
        return false;
      } finally {
        if (epoch === this.epoch) {
          this.options.busy(false);
          this.running = undefined;
          this.request = undefined;
          if (this.pending) {
            this.pending = false;
            this.schedule(failed ? retry : 0);
          } else this.schedule(retry);
        }
      }
    };
    this.running = work();
    return this.running;
  }
  visibility(visible: boolean) {
    if (this.visible === visible) return;
    this.visible = visible;
    if (!visible) {
      this.epoch++;
      this.request?.abort();
      this.running = undefined;
      this.options.busy(false);
      clearTimeout(this.timer);
    } else if (this.active) void this.refresh();
  }
  mutation() {
    if (this.active && this.visible) void this.refresh();
  }
  stop() {
    this.active = false;
    this.epoch++;
    this.pending = false;
    this.request?.abort();
    this.request = undefined;
    this.running = undefined;
    clearTimeout(this.timer);
    this.options.busy(false);
  }
  dispose() {
    this.stop();
    this.channel?.close();
  }
}
