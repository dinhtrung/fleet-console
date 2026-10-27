import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import "./index.css";
import { AuthProvider } from "./auth/session";
import { ScopeProvider } from "./shell/scope";
import { ToastProvider } from "./components/ui/Toast";
import { router } from "./router";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // A failed request must show its failed state immediately; a silent retry would hide it.
      retry: false,
      staleTime: 5_000,
      refetchOnWindowFocus: false,
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <ScopeProvider>
          <ToastProvider>
            <RouterProvider router={router} />
          </ToastProvider>
        </ScopeProvider>
      </AuthProvider>
    </QueryClientProvider>
  </StrictMode>,
);
