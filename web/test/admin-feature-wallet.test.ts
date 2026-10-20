import { expect, test } from "bun:test";
import { resolve } from "node:path";

test("real admin feature forms keep persisted billing separate from effective wallet flags", () => {
    const result = Bun.spawnSync([process.execPath, "test", "./test/fixtures/admin-feature-wallet.harness.tsx"], { cwd: resolve(import.meta.dir, ".."), timeout: 30_000 });
    const output = new TextDecoder().decode(result.stdout) + new TextDecoder().decode(result.stderr);
    if (result.exitCode !== 0) console.error(output);
    expect(output).toContain("0 fail");
    expect(result.exitCode).toBe(0);
});
