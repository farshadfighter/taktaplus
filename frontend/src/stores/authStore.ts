import { create } from "zustand";

interface AuthState {
  accessToken: string | null;
  username: string | null;
  setAuth: (accessToken: string, username: string) => void;
  logout: () => void;
}

const STORAGE_KEY = "taktaplus.auth";

function loadInitial(): { accessToken: string | null; username: string | null } {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    return raw ? JSON.parse(raw) : { accessToken: null, username: null };
  } catch {
    return { accessToken: null, username: null };
  }
}

export const useAuthStore = create<AuthState>((set) => ({
  ...loadInitial(),
  setAuth: (accessToken, username) => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ accessToken, username }));
    set({ accessToken, username });
  },
  logout: () => {
    localStorage.removeItem(STORAGE_KEY);
    set({ accessToken: null, username: null });
  },
}));
