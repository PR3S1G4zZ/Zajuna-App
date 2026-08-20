import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// El core Go sirve la API en un puerto efímero. En desarrollo local se asume
// que ya está corriendo en 127.0.0.1:62301 (el mismo puerto usado durante el
// desarrollo del prototipo vanilla); en producción el build de esta app se
// sirve directamente desde el mismo origen que la API, así que las rutas
// relativas ('/api/...') funcionan sin proxy.
const CORE_DEV_PORT = process.env.ZAJUNA_CORE_PORT || '62301'

const apiProxy = {
  '/api': {
    target: `http://127.0.0.1:${CORE_DEV_PORT}`,
    changeOrigin: true,
  },
}

export default defineConfig({
  plugins: [react()],
  base: '/',
  // Inline PostCSS so Vite never walks to a machine-global config such as
  // C:\postcss.config.mjs. The project does not use Tailwind.
  css: {
    postcss: {
      plugins: [],
    },
  },
  server: { proxy: apiProxy },
  preview: { proxy: apiProxy },
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
  },
})
