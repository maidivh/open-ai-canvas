import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { Window } from "happy-dom";
import type { AxiosRequestConfig } from "axios";
import type { WalletSettings, WalletSummary } from "../../src/services/api/wallet";

const dom = new Window({ url: "http://localhost/" });
const originals = new Map<string, PropertyDescriptor | undefined>();
for (const [key, value] of Object.entries({ window: dom, document: dom.document, navigator: dom.navigator, localStorage: dom.localStorage, HTMLElement: dom.HTMLElement, HTMLInputElement: dom.HTMLInputElement, Element: dom.Element, Node: dom.Node, Text: dom.Text, SVGElement: dom.SVGElement, ShadowRoot: dom.ShadowRoot, CustomEvent: dom.CustomEvent, getComputedStyle: dom.getComputedStyle.bind(dom), ResizeObserver: dom.ResizeObserver, IS_REACT_ACT_ENVIRONMENT: true })) {
    originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { App, ConfigProvider } = await import("antd");
const { WorkspaceWalletModal } = await import("../../src/components/layout/workspace-wallet-modal");
const { WalletSettingsDrawer } = await import("../../src/pages/admin/components/wallet-settings-drawer");
const { useUserStore, defaultFeatureAvailability } = await import("../../src/stores/use-user-store");
const { apiClient } = await import("../../src/services/api/request");
const { getAdminWalletSettings } = await import("../../src/services/api/wallet");
const originalAdapter = apiClient.defaults.adapter;

const external: WalletSummary = { walletMode: "external", rechargeUrl: "https://wallet.example/ai/recharge", account: { userId: "member", availableMicrocredits: 125_000_000, reservedMicrocredits: 0, version: 1, createdAt: "", updatedAt: "" }, entries: [{ id: "entry", userId: "member", type: "consume", amountMicrocredits: -1_000_000, availableAfterMicrocredits: 125_000_000, reservedAfterMicrocredits: 0, createdAt: "2026-10-19T00:00:00Z" }], total: 1, page: 1, pageSize: 20, policy: { signupBonusMicrocredits: 0, checkinBonusMicrocredits: 0, checkedInToday: false } };
const settings = { mode: "external" as const, baseUrl: "https://wallet.example", clientId: "canvas", siteId: 1, issuer: "cloooud", rechargeUrl: external.rechargeUrl!, secretConfigured: true, version: 3 };
let calls: AxiosRequestConfig[] = [];
let currentWallet = external;
let currentSettings: WalletSettings = settings;
let failWallet = false;
let root: ReturnType<typeof createRoot> | undefined;
let host: HTMLDivElement | undefined;

beforeEach(() => {
    calls = []; failWallet = false; currentWallet = external; currentSettings = settings;
    useUserStore.getState().setUser({ id: "member", username: "member", displayName: "Member", role: "admin", status: "active" });
    useUserStore.getState().setFeatures({ ...defaultFeatureAvailability, walletMode: "external", rechargeUrl: external.rechargeUrl });
    apiClient.defaults.adapter = async (config) => {
        calls.push(config);
        let data: unknown;
        if (config.url === "/wallet") {
            if (failWallet) throw new Error("小洞AI暂时无法连接");
            data = currentWallet;
        } else if (config.url === "/payments/products") data = { products: [] };
        else if (config.url === "/payments/providers") data = { providers: [] };
        else if (config.url === "/admin/wallet-settings") {
            if (config.method === "put") {
                const body = JSON.parse(config.data);
                if (body.version !== currentSettings.version) throw new Error("钱包配置已变化，请刷新后重试");
                currentSettings = { ...currentSettings, ...body, version: currentSettings.version + 1 };
            }
            data = currentSettings;
        }
        else throw new Error(`Unexpected request: ${config.url}`);
        return { data: { code: 0, data, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => { if (root) await act(async () => root!.unmount()); host?.remove(); root = undefined; });
afterAll(() => { apiClient.defaults.adapter = originalAdapter; dom.happyDOM.abort(); for (const [key, descriptor] of originals) { if (descriptor) Object.defineProperty(globalThis, key, descriptor); else Reflect.deleteProperty(globalThis, key); } });
const tick = () => new Promise((resolve) => setTimeout(resolve, 10));
async function showWallet() {
    await act(async () => { root!.render(<ConfigProvider theme={{ token: { motion: false } }}><App><WorkspaceWalletModal open onClose={() => {}} /></App></ConfigProvider>); });
    await act(tick);
}
function button(label: string) { return [...document.querySelectorAll("button")].find((item) => item.textContent?.replaceAll(" ", "").includes(label)); }

test("external wallet links to Xiaodong and never fetches internal topup providers", async () => {
    await showWallet();
    expect(document.body.textContent).toContain("125");
    expect(document.querySelector('a[href="https://wallet.example/ai/recharge"]')?.textContent).toContain("前往小洞AI充值");
    expect([...document.querySelectorAll('[role="tab"]')].map((node) => node.textContent)).not.toContain("兑换");
    expect(calls.some((call) => call.url?.startsWith("/payments/"))).toBe(false);
    await act(async () => button("积分消耗历史")!.click());
    expect(document.body.textContent).toContain("模型调用 · 小洞AI");
    expect(document.body.textContent).toContain("仅展示 Canvas");
});

test("unavailable external balance keeps recharge available without pretending it is zero", async () => {
    failWallet = true;
    await showWallet();
    expect(document.querySelector(".workspace-wallet-balance strong")?.textContent).toBe("暂不可用");
    expect(document.body.textContent).toContain("小洞AI暂时无法连接");
    expect(document.querySelector('a[href="https://wallet.example/ai/recharge"]')).not.toBeNull();
    expect(button("立即充值")).toBeUndefined();
});

test("refresh failure removes a previous balance instead of keeping stale spendable credits", async () => {
    await showWallet(); failWallet = true;
    await act(async () => { button("刷新余额")!.click(); });
    await act(tick);
    expect(document.querySelector(".workspace-wallet-balance strong")?.textContent).toBe("暂不可用");
});

test("internal wallet retains local recharge and redemption", async () => {
    currentWallet = { ...external, walletMode: "internal", rechargeUrl: "", account: { ...external.account, availableMicrocredits: 0 } };
    useUserStore.getState().setFeatures({ ...defaultFeatureAvailability, walletMode: "internal" });
    await showWallet();
    expect(document.querySelector(".workspace-wallet-balance strong")?.textContent).toBe("0");
    expect(calls.some((call) => call.url === "/payments/products")).toBe(true);
    expect(calls.some((call) => call.url === "/payments/providers")).toBe(true);
    const redeem = [...document.querySelectorAll<HTMLButtonElement>('[role="tab"]')].find((tab) => tab.textContent === "兑换")!;
    expect(redeem).toBeDefined(); await act(async () => redeem.click());
    expect(document.body.textContent).toContain("兑换码");
});

test("admin settings use direct GET/PUT contract and preserve a configured secret when left blank", async () => {
    expect(await getAdminWalletSettings()).toEqual(settings);
    let saved = false;
    await act(async () => { root!.render(<ConfigProvider theme={{ token: { motion: false } }}><App><WalletSettingsDrawer open onClose={() => {}} settings={settings} loading={false} error={null} onReload={() => {}} onSaved={() => { saved = true; }} /></App></ConfigProvider>); });
    await act(tick);
    expect(document.querySelector<HTMLInputElement>('input[type="password"]')?.value).toBe("");
    await act(async () => button("保存并校验")!.click()); await act(tick);
    expect(saved).toBe(true);
    const request = calls.find((call) => call.method === "put")!;
    expect(request.url).toBe("/admin/wallet-settings");
    expect(JSON.parse(request.data)).toEqual({ mode: "external", baseUrl: "https://wallet.example", clientId: "canvas", clientSecret: "", siteId: 1, issuer: "cloooud", rechargeUrl: external.rechargeUrl, version: 3 });

    // Reopening after a successful save must submit the newly fetched CAS version.
    const refreshed = await getAdminWalletSettings();
    expect(refreshed.version).toBe(4); saved = false;
    await act(async () => { root!.render(<ConfigProvider theme={{ token: { motion: false } }}><App><WalletSettingsDrawer open onClose={() => {}} settings={refreshed} loading={false} error={null} onReload={() => {}} onSaved={() => { saved = true; }} /></App></ConfigProvider>); });
    await act(tick);
    await act(async () => button("保存并校验")!.click()); await act(tick);
    expect(saved).toBe(true);
    expect(currentSettings.version).toBe(5);
    expect(calls.filter((call) => call.method === "put").map((call) => JSON.parse(call.data).version)).toEqual([3, 4]);
});
