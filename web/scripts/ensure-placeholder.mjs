// Vite empties dist/ on every build. The Go embed directive needs at least one
// file in a fresh checkout, so keep a placeholder next to the built assets.
import { mkdirSync, writeFileSync } from "node:fs";

mkdirSync("dist", { recursive: true });
writeFileSync(
  "dist/placeholder.txt",
  "Build the UI with: cd web && npm install && npm run build\n",
);
