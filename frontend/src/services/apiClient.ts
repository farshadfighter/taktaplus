import axios, { AxiosError, InternalAxiosRequestConfig } from "axios";

import { useAuthStore } from "@/stores/authStore";

export const apiClient = axios.create({
  baseURL: "/api/v1",
});

apiClient.interceptors.request.use((config) => {
  const token = useAuthStore.getState().accessToken;
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

interface RetryableConfig extends InternalAxiosRequestConfig {
  _retriedAfterRefresh?: boolean;
}

// Shared across concurrent 401s so a burst of requests triggers exactly one
// refresh call instead of one per request.
let refreshPromise: Promise<string | null> | null = null;

async function refreshAccessToken(): Promise<string | null> {
  const { refreshToken, username, setAuth } = useAuthStore.getState();
  if (!refreshToken) return null;
  try {
    // Plain axios, not apiClient - this must not carry the (expired) access
    // token header, and must not re-enter this same response interceptor.
    const { data } = await axios.post("/api/v1/auth/refresh", { refresh_token: refreshToken });
    setAuth(data.access_token, data.refresh_token, username ?? "");
    return data.access_token as string;
  } catch {
    return null;
  }
}

apiClient.interceptors.response.use(
  (response) => response,
  async (error: AxiosError) => {
    const config = error.config as RetryableConfig | undefined;
    const isAuthEndpoint = config?.url?.includes("/auth/login") || config?.url?.includes("/auth/refresh");

    if (error.response?.status === 401 && config && !config._retriedAfterRefresh && !isAuthEndpoint) {
      config._retriedAfterRefresh = true;
      if (!refreshPromise) {
        refreshPromise = refreshAccessToken().finally(() => {
          refreshPromise = null;
        });
      }
      const newAccessToken = await refreshPromise;
      if (newAccessToken) {
        config.headers.Authorization = `Bearer ${newAccessToken}`;
        return apiClient(config);
      }
    }

    if (error.response?.status === 401) {
      useAuthStore.getState().logout();
    }
    return Promise.reject(error);
  },
);
