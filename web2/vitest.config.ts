import { defineConfig } from 'vitest/config'
import { fileURLToPath, URL } from 'node:url'

// Vitest covers the store/composable layer (Pinia + Vue reactivity) that the pure
// `node --test` lib suite can't (it needs bundler resolution for extensionless/
// aliased imports). Pure lib tests stay on node --test (*.test.ts); store tests
// are *.spec.ts here.
export default defineConfig({
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  test: {
    include: ['src/**/*.spec.ts'],
    environment: 'node',
  },
})
