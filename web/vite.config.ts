import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The Go binary embeds dist/app (see embed.go); dist/.gitkeep keeps the
// directory present so the Go build works before the UI is built.
export default defineConfig({
  plugins: [react()],
  build: { outDir: 'dist/app', emptyOutDir: true, chunkSizeWarningLimit: 1500 },
  server: { proxy: { '/api': 'http://localhost:8080', '/healthz': 'http://localhost:8080' } },
})
