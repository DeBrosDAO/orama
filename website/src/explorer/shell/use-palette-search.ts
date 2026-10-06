import { useEffect, useMemo, useState } from "react";
import { useQuery } from "../data/use-query";
import type { WalletRef } from "../model/types";
import { classify } from "../model/search";
import type { Hit } from "../model/search";
import { LABEL_SEARCH_DEBOUNCE_MS, MAX_LABEL_RESULTS, buildRows, labelQueryOf, shouldSearchLabels } from "./palette-model";
import type { PaletteRow } from "./palette-model";

const NO_LABELS: readonly WalletRef[] = [];

/** `value`, but only after it has stopped changing for `ms`. */
function useDebounced<T>(value: T, ms: number): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const t = window.setTimeout(() => setSettled(value), ms);
    return () => window.clearTimeout(t);
  }, [value, ms]);
  return settled;
}

export interface PaletteSearch {
  trimmed: string;
  hit: Hit | null;
  rows: PaletteRow[];
  /** Name matches are still on their way, so an empty list does not yet mean "nothing". */
  pending: boolean;
  /** Why the name search failed, or null. */
  error: Error | null;
}

/** What the palette shows for `query`: an exact hit, name matches (debounced) or shortcuts. */
export function usePaletteSearch(query: string): PaletteSearch {
  const trimmed = query.trim();
  const hit = useMemo(() => classify(trimmed), [trimmed]);
  const wanted = shouldSearchLabels(trimmed, hit) ? labelQueryOf(trimmed) : null;
  const settled = useDebounced(wanted, LABEL_SEARCH_DEBOUNCE_MS);
  const labels = useQuery((s) => (settled ? s.searchLabels(settled, MAX_LABEL_RESULTS) : Promise.resolve([])), [settled]);
  const examples = useQuery((s) => s.getExamples(), []);

  const current = wanted !== null && settled === wanted;
  const labelList = current && labels.state.status === "ready" ? labels.state.data : NO_LABELS;
  const exampleData = examples.state.status === "ready" ? examples.state.data : null;
  const rows = useMemo(
    () => buildRows({ trimmed, hit, labels: labelList, examples: exampleData }),
    [trimmed, hit, labelList, exampleData],
  );
  const pending = wanted !== null && (!current || labels.state.status === "loading");
  const error = current && labels.state.status === "error" ? labels.state.error : null;
  return { trimmed, hit, rows, pending, error };
}
