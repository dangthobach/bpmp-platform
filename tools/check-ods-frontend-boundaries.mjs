import { readFile, readdir } from "node:fs/promises";
import { extname, join, relative, resolve } from "node:path";

const repositoryRoot = resolve(import.meta.dirname, "..");
const sourceRoots = [
  resolve(repositoryRoot, "apps/web/ods-shell/src"),
  resolve(repositoryRoot, "apps/web/task-mfe/src"),
];

const violations = [];

async function walk(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) files.push(...await walk(path));
    else if ([".ts", ".tsx"].includes(extname(entry.name))) files.push(path);
  }
  return files;
}

for (const root of sourceRoots) {
  for (const file of await walk(root)) {
    const source = await readFile(file, "utf8");
    const display = relative(repositoryRoot, file).replaceAll("\\", "/");
    const rules = [
      [/(?:from\s+|import\s*)["']@mui\//, "domain apps must import @ods/shared-ui, not MUI"],
      [/(?:from\s+|import\s*)["']@emotion\//, "domain apps must not import Emotion directly"],
      [/(?:from\s+|import\s*)["']axios["']/, "domain apps must use the injected callApi contract"],
      [/(?:from\s+|import\s*)["'][^"']*apps\/web\//, "apps must not import another app source tree"],
    ];
    for (const [pattern, message] of rules) {
      if (pattern.test(source)) violations.push(`${display}: ${message}`);
    }
  }
}

if (violations.length > 0) {
  process.stderr.write(`${violations.join("\n")}\n`);
  process.exitCode = 1;
} else {
  process.stdout.write("ODS frontend dependency boundaries passed.\n");
}
