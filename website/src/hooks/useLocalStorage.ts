import { useCallback, useEffect, useState } from "react";

function readValue<T>(key: string, defaultValue: T): T {
  if (typeof window === "undefined") return defaultValue;
  try {
    const raw = localStorage.getItem(key);
    if (raw === null) return defaultValue;
    return JSON.parse(raw) as T;
  } catch {
    return defaultValue;
  }
}

export function useLocalStorage<T>(key: string, defaultValue: T): [T, (value: T) => void] {
  const [storedValue, setStoredValue] = useState<T>(() => readValue(key, defaultValue));

  const setValue = useCallback(
    (value: T) => {
      setStoredValue(value);
      try {
        localStorage.setItem(key, JSON.stringify(value));
      } catch {
        // localStorage unavailable or quota exceeded
      }
    },
    [key],
  );

  useEffect(() => {
    const handleStorage = (e: StorageEvent) => {
      if (e.key === key) {
        setStoredValue(e.newValue === null ? defaultValue : (JSON.parse(e.newValue) as T));
      }
    };
    window.addEventListener("storage", handleStorage);
    return () => window.removeEventListener("storage", handleStorage);
  }, [key, defaultValue]);

  return [storedValue, setValue];
}
