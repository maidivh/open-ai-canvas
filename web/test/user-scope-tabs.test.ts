import { afterAll, expect, test } from "bun:test";
import { ACTIVE_USER_SCOPE_KEY, getActiveUserScope, scopedLocalStorage, setActiveUserScope } from "../src/lib/user-scope";
import { apiClient } from "../src/services/api/request";

const previousWindow = globalThis.window;
const values = new Map<string, string>();
globalThis.window = { localStorage: {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value); },
    removeItem: (key: string) => { values.delete(key); },
} } as unknown as Window & typeof globalThis;
afterAll(() => { globalThis.window = previousWindow; });

test("another tab switching accounts cannot redirect old in-memory data into the new user's storage", () => {
    setActiveUserScope("old-user");
    window.localStorage.setItem(ACTIVE_USER_SCOPE_KEY, "new-user");
    scopedLocalStorage.setItem("draft", "old user's work");
    expect(getActiveUserScope()).toBe("old-user");
    expect(values.get("draft:user:old-user")).toBe("old user's work");
    expect(values.has("draft:user:new-user")).toBe(false);
    setActiveUserScope("new-user");
    expect(getActiveUserScope()).toBe("new-user");
});

test("actual API requests keep the loaded tab's identity after a shared-cookie account switch", async () => {
    setActiveUserScope("old-user");
    window.localStorage.setItem(ACTIVE_USER_SCOPE_KEY, "new-user");
    let identity: unknown;
    await apiClient.post("/projects", { title: "old draft" }, { adapter: async (config) => {
        identity = config.headers.get("X-Canvas-User-ID");
        return { data: {}, status: 200, statusText: "OK", headers: {}, config };
    } });
    expect(identity).toBe("old-user");
});
