import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { isDeepStrictEqual } from "node:util";

// Older documentation generators used CRLF or omitted the final newline.
function normalizeDocument(text) {
  return text.replace(/\r\n/g, "\n").replace(/\n$/, "");
}

function entries(root, dir = "", included) {
  return readdirSync(join(root, dir), { withFileTypes: true }).filter((entry) => !included || included.has(entry.name)).flatMap((entry) => {
    const path = join(dir, entry.name);
    return entry.isDirectory() ? entries(root, path) : [path];
  }).sort();
}

function sameFile(path, left, right) {
  // Some old ZIPs marked manifests and Markdown executable. Only payload modes affect execution.
  const documentation = path === "README.md" || path === "docs/interface.md";
  if (!documentation && path !== "manifest.json" && (statSync(left).mode & 0o111) !== (statSync(right).mode & 0o111)) return false;
  const a = readFileSync(left);
  const b = readFileSync(right);
  if (a.equals(b)) return true;
  if (documentation) {
    return normalizeDocument(a.toString()) === normalizeDocument(b.toString());
  }
  if (path !== "manifest.json") return false;
  const manifests = [a, b].map((source) => {
    const manifest = JSON.parse(source);
    if (typeof manifest.documentation === "string") manifest.documentation = normalizeDocument(manifest.documentation);
    return manifest;
  });
  return isDeepStrictEqual(...manifests);
}

const [left, right, ...inputs] = process.argv.slice(2);
const paths = entries(left, "", new Set(inputs));
const identical = isDeepStrictEqual(paths, entries(right)) && paths.every((path) => sameFile(path, join(left, path), join(right, path)));
process.exitCode = identical ? 0 : 1;
