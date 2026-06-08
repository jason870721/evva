import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// base: './' makes built asset URLs relative, so the bundle works when served
// from the embedded FS inside `evva service` regardless of mount path.
export default defineConfig({
  plugins: [vue()],
  base: './',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // Stable (un-hashed) asset names. dist/ is vendored into the repo so the
    // single binary embeds a working UI without a node build step at install
    // time; stable names keep rebuilds to content-only diffs instead of
    // churning hash-named files. Cache-busting is a non-issue for a localhost
    // workstation served by `evva service`.
    rollupOptions: {
      output: {
        entryFileNames: 'assets/[name].js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/[name].[ext]',
      },
    },
  },
})
