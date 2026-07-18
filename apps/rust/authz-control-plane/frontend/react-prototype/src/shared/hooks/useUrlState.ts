// Synchronises a small, typed slice of state with the URL query string so that
// pagination, filters, and selection survive page reloads and can be shared
// via copy/paste. Backed by react-router's `useSearchParams`.
import { useCallback } from "react";
import { useSearchParams } from "react-router-dom";

export function useUrlString(
  key: string,
  defaultValue: string,
): [string, (next: string) => void] {
  const [params, setParams] = useSearchParams();
  const value = params.get(key) ?? defaultValue;
  const set = useCallback(
    (next: string) => {
      const p = new URLSearchParams(params);
      if (next === defaultValue) p.delete(key);
      else p.set(key, next);
      setParams(p, { replace: true });
    },
    [defaultValue, key, params, setParams],
  );
  return [value, set];
}

export function useUrlNumber(
  key: string,
  defaultValue: number,
): [number, (next: number) => void] {
  const [s, set] = useUrlString(key, String(defaultValue));
  return [Number.parseInt(s, 10) || defaultValue, (n) => set(String(n))];
}
