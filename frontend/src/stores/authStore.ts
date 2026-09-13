import { create } from "zustand";

interface AuthState {
  accessToken: string | null;
  refreshToken: string | null;
  username: string | null;
  setAuth: (accessToken: string, refreshToken: string, username: string) => void;
  setAccessToken: (accessToken: string) => void;
  logout: () => void;
}

interface StoredAuth {
  accessToken: string | null;
  refreshToken: string | null;
  username: string | null;
}

const STORAGE_KEY = "taktaplus.auth";

function loadInitial(): StoredAuth {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return { accessToken: null, refreshToken: null, username: null };
    const parsed = JSON.parse(raw);
    return { accessToken: parsed.accessToken ?? null, refreshToken: parsed.refreshToken ?? null, username: parsed.username ?? null };
  } catch {
    return { accessToken: null, refreshToken: null, username: null };
  }
}

function persist(state: StoredAuth) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
}

export const useAuthStore = create<AuthState>((set, get) => ({
  ...loadInitial(),
  setAuth: (accessToken, refreshToken, username) => {
    persist({ accessToken, refreshToken, username });
    set({ accessToken, refreshToken, username });
  },
  setAccessToken: (accessToken) => {
    persist({ accessToken, refreshToken: get().refreshToken, username: get().username });
    set({ accessToken });
  },
  logout: () => {
    localStorage.removeItem(STORAGE_KEY);
    set({ accessToken: null, refreshToken: null, username: null });
  },
}));
