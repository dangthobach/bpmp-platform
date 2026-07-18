// Generic SelectionState for table-style bulk actions.
// O(1) toggle, O(k) `selectedIds`. Persists across pagination because it is
// keyed by row id (not row index).
import { useCallback, useMemo, useState } from "react";

export interface SelectionState<Id extends string = string> {
  selected: ReadonlySet<Id>;
  isSelected: (id: Id) => boolean;
  toggle: (id: Id) => void;
  toggleMany: (ids: Id[], next: boolean) => void;
  clear: () => void;
  count: number;
}

export function useSelection<Id extends string = string>(): SelectionState<Id> {
  const [selected, setSelected] = useState<Set<Id>>(() => new Set());

  const toggle = useCallback((id: Id) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const toggleMany = useCallback((ids: Id[], on: boolean) => {
    setSelected((prev) => {
      const next = new Set(prev);
      for (const id of ids) {
        if (on) next.add(id);
        else next.delete(id);
      }
      return next;
    });
  }, []);

  const clear = useCallback(() => setSelected(new Set()), []);

  return useMemo(
    () => ({
      selected,
      isSelected: (id) => selected.has(id),
      toggle,
      toggleMany,
      clear,
      count: selected.size,
    }),
    [selected, toggle, toggleMany, clear],
  );
}
