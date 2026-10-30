import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { dependencyInventory } from './build/dependency-inventory.ts';

export default defineConfig({
  plugins: [react(), dependencyInventory()],
  build: { target: 'es2022', sourcemap: false },
  server: {
    strictPort: true,
    proxy: { '/api': { target: process.env.AIVIDCUT_BACKEND || 'http://127.0.0.1:43110' } },
  },
});
