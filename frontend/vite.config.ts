import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: { '/api': 'http://localhost:8080', '/healthz': 'http://localhost:8080' },
  },
  // Swagger UI (~1.5 Mo) est un chunk à part, chargé seulement à l'ouverture de la documentation d'une API.
  build: { outDir: 'dist', sourcemap: false, chunkSizeWarningLimit: 1600 },
})
