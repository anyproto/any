import { createJSONStorage } from 'jotai/utils';
// jotai re-exports the function but not the type aliases at the
// top-level entry; pull the types from the deep path directly.
import type {
  SyncStringStorage,
  SyncStorage,
} from 'jotai/vanilla/utils/atomWithStorage';

const memoryStorage = new Map<string, string>();

const fallbackStringStorage: SyncStringStorage = {
  getItem: (key) => memoryStorage.get(key) ?? null,
  setItem: (key, value) => {
    memoryStorage.set(key, value);
  },
  removeItem: (key) => {
    memoryStorage.delete(key);
  },
};

function isSyncStringStorage(value: unknown): value is SyncStringStorage {
  return (
    value !== null &&
    typeof value === 'object' &&
    typeof (value as Partial<SyncStringStorage>).getItem === 'function' &&
    typeof (value as Partial<SyncStringStorage>).setItem === 'function' &&
    typeof (value as Partial<SyncStringStorage>).removeItem === 'function'
  );
}

function getStringStorage(): SyncStringStorage {
  if (typeof window === 'undefined') return fallbackStringStorage;
  try {
    const candidate = window.localStorage;
    if (isSyncStringStorage(candidate)) return candidate;
  } catch (err) {
    // localStorage can throw in constrained browser contexts.
    warnStorageFallback(err);
    return fallbackStringStorage;
  }
  warnStorageFallback(new Error('window.localStorage is not a sync string storage'));
  return fallbackStringStorage;
}

export function appStorage<Value>(): SyncStorage<Value> {
  return createJSONStorage<Value>(getStringStorage);
}

function warnStorageFallback(err: unknown) {
  if ((import.meta as { env?: { DEV?: boolean } }).env?.DEV) {
    console.warn('Falling back to in-memory app storage', err);
  }
}
