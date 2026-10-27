import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    // Deep links are part of the contract: a static host needs an index.html fallback, and absolute
    // asset URLs keep a nested route resolvable.
    outDir: 'dist',
    sourcemap: false,
  },
})
