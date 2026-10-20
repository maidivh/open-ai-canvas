import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "http://localhost/" });
const originals = new Map<string, PropertyDescriptor | undefined>();
for (const [key, value] of Object.entries({ window: dom, document: dom.document, navigator: dom.navigator, localStorage: dom.localStorage, HTMLElement: dom.HTMLElement, HTMLInputElement: dom.HTMLInputElement, HTMLAnchorElement: dom.HTMLAnchorElement, MouseEvent: dom.MouseEvent, Element: dom.Element, Node: dom.Node, Text: dom.Text, SVGElement: dom.SVGElement, ShadowRoot: dom.ShadowRoot, CustomEvent: dom.CustomEvent, getComputedStyle: dom.getComputedStyle.bind(dom), ResizeObserver: dom.ResizeObserver, IS_REACT_ACT_ENVIRONMENT: true })) {
    originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { App, ConfigProvider } = await import("antd");
const { default: FeatureAvailabilityPanel } = await import("../../src/pages/admin/components/feature-availability-panel");
const { WelcomeSetting } = await import("../../src/pages/admin/settings/components/welcome-setting");
const { useUserStore, defaultFeatureAvailability } = await import("../../src/stores/use-user-store");
const { apiClient } = await import("../../src/services/api/request");
const originalAdapter = apiClient.defaults.adapter;

let persisted = { ...defaultFeatureAvailability, creditsEnabled: false };
let mode: "internal" | "external" = "external";
let includeConfigured = true;
let omitConfiguredCredits = false;
let patches: Record<string, unknown>[] = [];
let root: ReturnType<typeof createRoot>;
let host: HTMLDivElement;

beforeEach(() => {
    persisted = { ...defaultFeatureAvailability, creditsEnabled: false };
    mode = "external"; includeConfigured = true; omitConfiguredCredits = false; patches = [];
    useUserStore.getState().setFeatures(defaultFeatureAvailability);
    apiClient.defaults.adapter = async (config) => {
        if (config.url !== "/admin/settings/features") throw new Error(`Unexpected request: ${config.url}`);
        if (config.method === "patch") {
            const body = JSON.parse(config.data);
            patches.push(body);
            persisted = { ...persisted, ...body, creditsEnabled: mode === "external" ? persisted.creditsEnabled : body.creditsEnabled ?? persisted.creditsEnabled };
        }
        const configured: Partial<typeof persisted> = { ...persisted };
        if (omitConfiguredCredits) delete configured.creditsEnabled;
        const features = {
            ...persisted, walletMode: mode, creditsEnabled: mode === "external" || persisted.creditsEnabled,
            ...(includeConfigured ? { configuredFeatures: configured } : {}),
        };
        return { data: { code: 0, data: { features }, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
afterAll(() => { apiClient.defaults.adapter = originalAdapter; dom.happyDOM.abort(); for (const [key, descriptor] of originals) { if (descriptor) Object.defineProperty(globalThis, key, descriptor); else Reflect.deleteProperty(globalThis, key); } });
const tick = () => new Promise((resolve) => setTimeout(resolve, 10));
async function render(welcome = false) {
    await act(async () => { root.render(<ConfigProvider theme={{ token: { motion: false } }}><App>{welcome ? <WelcomeSetting /> : <FeatureAvailabilityPanel />}</App></ConfigProvider>); });
    await act(tick);
}
function toggle(label: string) {
    const control = document.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`);
    expect(control).not.toBeNull();
    return control!;
}
async function changeTaskCenter() {
    await act(async () => toggle("设置任务中心，切换后立即保存").click());
    await act(tick);
}

for (const original of [false, true]) {
    test(`stale external form preserves internal credits=${original} after another page switches mode`, async () => {
        persisted.creditsEnabled = original;
        await render();
        expect(useUserStore.getState().features.creditsEnabled).toBe(true);
        const credits = toggle("设置积分计费，切换后立即保存");
        expect(credits.disabled).toBe(true);
        expect(credits.getAttribute("aria-checked")).toBe("true");
        expect(credits.closest("article")?.textContent).toContain("已开放");
        mode = "internal";
        await changeTaskCenter();
        expect(patches).toHaveLength(1);
        expect(patches[0]!.creditsEnabled).toBe(original);
        expect(persisted.creditsEnabled).toBe(original);
        expect(persisted.taskCenterEnabled).toBe(false);
        expect(useUserStore.getState().features.creditsEnabled).toBe(original);
        expect(useUserStore.getState().features.walletMode).toBe("internal");
        expect(document.body.textContent).not.toContain("服务端返回的功能状态与本次保存内容不一致");
    });
}

test("stale internal form receives external effective state without replacing its saved policy", async () => {
    mode = "internal";
    await render();
    expect(useUserStore.getState().features.creditsEnabled).toBe(false);
    mode = "external";
    await changeTaskCenter();
    expect(patches[0]!.creditsEnabled).toBe(false);
    expect(persisted.creditsEnabled).toBe(false);
    expect(useUserStore.getState().features.creditsEnabled).toBe(true);
    expect(useUserStore.getState().features.walletMode).toBe("external");
    expect(document.body.textContent).not.toContain("服务端返回的功能状态与本次保存内容不一致");
});

test("internal credits can still be explicitly enabled", async () => {
    mode = "internal";
    await render();
    await act(async () => toggle("设置积分计费，切换后立即保存").click());
    await act(tick);
    expect(patches[0]!.creditsEnabled).toBe(true);
    expect(persisted.creditsEnabled).toBe(true);
    expect(useUserStore.getState().features.creditsEnabled).toBe(true);
});

test("external legacy response without persisted settings cannot become an editable form", async () => {
    includeConfigured = false;
    await render();
    expect(document.body.textContent).toContain("持久");
    expect(document.querySelector('button[aria-label="设置任务中心，切换后立即保存"]')).toBeNull();
    expect(patches).toHaveLength(0);
});

test("internal legacy response remains editable", async () => {
    includeConfigured = false; mode = "internal";
    await render();
    await changeTaskCenter();
    expect(persisted.creditsEnabled).toBe(false);
    expect(persisted.taskCenterEnabled).toBe(false);
});

test("incomplete persisted settings cannot fall back to effective credits", async () => {
    omitConfiguredCredits = true;
    await render();
    expect(document.body.textContent).toContain("缺少有效开关");
    expect(document.querySelector('button[aria-label="设置任务中心，切换后立即保存"]')).toBeNull();
    expect(patches).toHaveLength(0);
});

test("welcome consumer only patches welcome and keeps the effective response in the global store", async () => {
    await render(true);
    await act(async () => toggle("启用欢迎页").click());
    await act(tick);
    expect(patches).toEqual([{ welcomeEnabled: false }]);
    expect(persisted.creditsEnabled).toBe(false);
    expect(useUserStore.getState().features.creditsEnabled).toBe(true);
    expect(useUserStore.getState().features.walletMode).toBe("external");
});
