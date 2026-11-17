import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import "./index.css";
import { AppToastProvider } from "./components/ui";
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
      <AppToastProvider>
        <RouterProvider router={router} />
      </AppToastProvider>
    </QueryClientProvider>
  </StrictMode>,
);
