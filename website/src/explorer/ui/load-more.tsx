import type { KeyPaged } from "./use-key-paged";

/** The "show more" control under a key-paged list: the button, what it is loading, and why it failed. */
export function LoadMore<T>({ paged, what }: { paged: Pick<KeyPaged<T>, "hasMore" | "loadingMore" | "moreError" | "loadMore">; what: string }) {
  const { hasMore, loadingMore, moreError, loadMore } = paged;
  if (!hasMore && moreError === null) return null;
  return (
    <div className="flex flex-wrap items-center justify-center gap-3 pt-3">
      {moreError !== null && (
        <p role="alert" className="text-xs text-loss">
          Could not load more {what}: {moreError.message}
        </p>
      )}
      {hasMore && (
        <button
          type="button"
          onClick={loadMore}
          disabled={loadingMore}
          className="rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm enabled:hover:border-fg/30 enabled:cursor-pointer disabled:opacity-60"
        >
          {loadingMore ? "Loading…" : moreError !== null ? "Try again" : `Show more ${what}`}
        </button>
      )}
    </div>
  );
}
