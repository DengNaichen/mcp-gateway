import { defineConfig, loadEnv } from "vite";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, ".", "VITE_");
  const apiPort = env.VITE_API_PORT || "8789";
  return {
    server: {
      proxy: { "/api": `http://127.0.0.1:${apiPort}`, "/mcp": `http://127.0.0.1:${apiPort}` },
    },
  };
});
