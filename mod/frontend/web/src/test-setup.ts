import '@testing-library/jest-dom/vitest';

function storageMock() {
  const storage = new Map<string, string>();
  return {
    get length() {
      return storage.size;
    },
    key(index: number) {
      return [...storage.keys()][index] ?? null;
    },
    getItem(key: string) {
      return storage.has(String(key)) ? storage.get(String(key)) : null;
    },
    setItem(key: string, value: string) {
      storage.set(String(key), String(value));
    },
    removeItem(key: string) {
      storage.delete(String(key));
    },
    clear() {
      storage.clear();
    },
  };
}

const localStorageMock = storageMock();
const sessionStorageMock = storageMock();
Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: localStorageMock });
Object.defineProperty(window, 'localStorage', { configurable: true, value: localStorageMock });
Object.defineProperty(globalThis, 'sessionStorage', { configurable: true, value: sessionStorageMock });
Object.defineProperty(window, 'sessionStorage', { configurable: true, value: sessionStorageMock });
