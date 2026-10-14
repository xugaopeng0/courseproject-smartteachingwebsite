import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: { "/api": "http://127.0.0.1:8081" },
    // 编辑器保存源码时会写临时目录，Windows 上监听这些路径会以 EBUSY 崩掉整个 dev server。
    watch: { ignored: ["**/.*.tmpdir/**", "**/*.tmp", "**/*.tmpdir/**"] },
  },
});
