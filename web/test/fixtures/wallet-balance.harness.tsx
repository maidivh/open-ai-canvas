import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { Window } from "happy-dom";
import type { AxiosRequestConfig } from "axios";
import type { WalletSummary } from "../../src/services/api/wallet";

const dom = new Window({ url: "http://localhost/" });
const originals = new Map<string, PropertyDescriptor | undefined>();
for (const [key, value] of Object.entries({ window: dom, document: dom.document, navigator: dom.navigator, localStorage: dom.localStorage, HTMLElement: dom.HTMLElement, Element: dom.Element, Node: dom.Node, Text: dom.Text, SVGElement: dom.SVGElement, ShadowRoot: dom.ShadowRoot, CustomEvent: dom.CustomEvent, getComputedStyle: dom.getComputedStyle.bind(dom), ResizeObserver: dom.ResizeObserver, IS_REACT_ACT_ENVIRONMENT: true })) {
    originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { App, ConfigProvider } = await import("antd");
const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
const { useWalletBalance } = await import("../../src/hooks/use-wallet-balance");
const { useUserStore, defaultFeatureAvailability } = await import("../../src/stores/use-user-store");
const { ModelPicker } = await import("../../src/components/model-picker");
const { WorkspaceWalletModal } = await import("../../src/components/layout/workspace-wallet-modal");
const { WorkspaceAccountCard } = await import("../../src/components/layout/workspace-account-card");
const { ProfilePane } = await import("../../src/pages/settings/profile-pane");
const { MemoryRouter } = await import("react-router");
const { loadProfileSession } = await import("../../src/services/profile-session");
const { createModelChannel, defaultConfig, normalizeConfigSnapshot } = await import("../../src/stores/use-config-store");
const { apiClient } = await import("../../src/services/api/request");
const { refreshFeatureAvailability } = await import("../../src/lib/user-session");
const originalAdapter = apiClient.defaults.adapter;
const originalWarn = console.warn;
const channel = createModelChannel({ id: "system", name: "System", scope: "system", baseUrl: "/api/system-channel", apiKey: "system", models: ["image-model"], modelCosts: [{ model: "image-model", capability: "image", billingMode: "fixed_request", unitPriceMicrocredits: 200_000 }] });
const config = normalizeConfigSnapshot({ config: { ...defaultConfig, channels: [channel], model: "system::image-model", imageModel: "system::image-model", count: "1" } }).config;
const user = (id: string) => ({ id, username: id, displayName: id, role: "user" as const, status: "active" as const });
function wallet(mode: "internal" | "external", userId = "member"): WalletSummary {
    return { walletMode: mode, rechargeUrl: mode === "external" ? "https://wallet.example/ai" : "", account: { userId, availableMicrocredits: mode === "external" ? 100_000_000 : 10_000_000, reservedMicrocredits: 0, version: 1, createdAt: "", updatedAt: "" }, policy: { signupBonusMicrocredits: 0, checkinBonusMicrocredits: 0, checkedInToday: false }, entries: [], total: 0, page: 1, pageSize: 1 };
}
let serverFeatures = { ...defaultFeatureAvailability, walletMode: "internal" as "internal" | "external" };
let serverWallet = wallet("internal");
let calls: AxiosRequestConfig[] = [];
let walletResponse: (() => Promise<WalletSummary>) | undefined;
let featureResponse: (() => Promise<typeof serverFeatures>) | undefined;
let quoteResponse: (() => Promise<number>) | undefined;
let failWallet = false;
let failFeatures = false;
let root: ReturnType<typeof createRoot>;
let host: HTMLDivElement;
let client: InstanceType<typeof QueryClient>;
let latest: ReturnType<typeof useWalletBalance>;
let warnings: unknown[][] = [];
const tick = () => new Promise((resolve) => setTimeout(resolve, 15));
function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => { resolve = done; });
    return { promise, resolve };
}
function Reader() {
    const currentUser = useUserStore((state) => state.user);
    const enabled = useUserStore((state) => state.features.creditsEnabled);
    latest = useWalletBalance(currentUser?.id, enabled);
    return <output>{latest.walletMode}:{latest.availableMicrocredits ?? "unavailable"}</output>;
}
beforeEach(() => {
    calls = []; warnings = []; failWallet = false; failFeatures = false; walletResponse = undefined; featureResponse = undefined; quoteResponse = undefined;
    serverFeatures = { ...defaultFeatureAvailability, walletMode: "internal" };
    serverWallet = wallet("internal");
    useUserStore.getState().setUser(user("member"));
    useUserStore.getState().setFeatures(serverFeatures);
    console.warn = (...args) => { warnings.push(args); };
    apiClient.defaults.adapter = async (request) => {
        calls.push(request);
        let data: unknown;
        if (request.url === "/features") {
            if (failFeatures) throw new Error("功能状态暂不可用");
            data = { features: featureResponse ? await featureResponse() : { ...serverFeatures } };
        } else if (request.url === "/wallet") {
            if (failWallet) throw new Error("钱包暂不可用");
            data = walletResponse ? await walletResponse() : serverWallet;
        } else if (request.url === "/model-catalog/quote") {
            data = { quote: { billingMode: "fixed_request", amountMicrocredits: quoteResponse ? await quoteResponse() : 1_000_000, estimated: false } };
        } else if (request.url === "/auth/profile") {
            data = { user: { ...user("member"), displayName: "Updated profile" }, hasPassword: true, emailEnabled: false, linuxdoEnabled: false };
        } else if (request.url === "/payments/products") data = { products: [] };
        else if (request.url === "/payments/providers") data = { providers: [] };
        else throw new Error(`Unexpected request: ${request.url}`);
        return { data: { code: 0, data, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config: request };
    };
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => {
    await act(async () => root.unmount()); client.clear(); host.remove();
    console.warn = originalWarn;
});
afterAll(() => {
    apiClient.defaults.adapter = originalAdapter; dom.happyDOM.abort();
    for (const [key, descriptor] of originals) {
        if (descriptor) Object.defineProperty(globalThis, key, descriptor);
        else Reflect.deleteProperty(globalThis, key);
    }
});
async function show(copies = 1, showWallet = false) {
    await act(async () => root.render(<QueryClientProvider client={client}><MemoryRouter>{Array.from({ length: copies }, (_, i) => <Reader key={i} />)}<ModelPicker config={config} value={config.model} capability="image" onChange={() => {}} />{showWallet ? <ConfigProvider theme={{ token: { motion: false } }}><App><WorkspaceWalletModal open onClose={() => {}} /></App></ConfigProvider> : null}</MemoryRouter></QueryClientProvider>));
    await act(tick);
}
async function event(name: "wallet:updated" | "focus" | "visibilitychange") {
    await act(async () => { (name === "visibilitychange" ? document : window).dispatchEvent(new CustomEvent(name)); });
    await act(tick);
}
function setServerMode(mode: "internal" | "external", creditsEnabled = true) {
    serverFeatures = { ...serverFeatures, walletMode: mode, creditsEnabled };
    serverWallet = wallet(mode);
}

test.each(["features", "wallet"] as const)("same-account profile refresh preserves the pending %s request", async (phase) => {
    setServerMode("external"); useUserStore.getState().setFeatures(serverFeatures);
    const pendingWallet = deferred<WalletSummary>();
    const pendingFeatures = deferred<typeof serverFeatures>();
    if (phase === "features") featureResponse = () => pendingFeatures.promise;
    else walletResponse = () => pendingWallet.promise;
    await show();
    await act(async () => { await loadProfileSession("member", new AbortController().signal); });
    await act(async () => { pendingFeatures.resolve(serverFeatures); pendingWallet.resolve(wallet("external")); });
    await act(tick);
    expect(useUserStore.getState().user?.displayName).toBe("Updated profile");
    expect(latest.error).toBeNull();
    expect(latest.availableMicrocredits).toBe(100_000_000);
});

test("real profile and account-card components preserve the balance when profile data arrives first", async () => {
    setServerMode("external"); useUserStore.getState().setFeatures(serverFeatures);
    const pending = deferred<WalletSummary>(); walletResponse = () => pending.promise;
    await act(async () => root.render(
        <QueryClientProvider client={client}><ConfigProvider theme={{ token: { motion: false } }}><App><MemoryRouter>
            <Reader /><ProfilePane /><WorkspaceAccountCard onWallet={() => {}} onNavigate={() => {}} />
        </MemoryRouter></App></ConfigProvider></QueryClientProvider>,
    ));
    await act(tick);
    await act(async () => { pending.resolve(wallet("external")); });
    await act(tick);
    expect(document.querySelector(".workspace-account-card-balance")?.textContent).toContain("100");
    expect(latest.error).toBeNull();
    expect(calls.filter((call) => call.url === "/auth/profile")).toHaveLength(1);
});

test("a pending public feature refresh survives an ordinary profile update", async () => {
    await show();
    const pending = deferred<typeof serverFeatures>(); featureResponse = () => pending.promise;
    const refresh = refreshFeatureAvailability();
    await act(async () => { await loadProfileSession("member", new AbortController().signal); });
    await act(async () => { pending.resolve({ ...serverFeatures, taskCenterEnabled: false }); await refresh; });
    expect(useUserStore.getState().features.taskCenterEnabled).toBe(false);
});

test.each(["relogin", "account round trip", "renewed session"] as const)("a %s fetches a fresh wallet and rejects the previous session snapshot", async (transition) => {
    setServerMode("external"); useUserStore.getState().setFeatures(serverFeatures);
    const old = deferred<WalletSummary>(); walletResponse = () => old.promise;
    await show();
    walletResponse = undefined;
    serverWallet = { ...wallet("external"), account: { ...wallet("external").account, availableMicrocredits: 50_000_000 } };
    await act(async () => {
        if (transition === "relogin") useUserStore.getState().clearSession();
        if (transition === "account round trip") useUserStore.getState().setUser(user("other"));
        useUserStore.getState().setUser(user("member"), { newSession: transition === "renewed session" });
        useUserStore.getState().setFeatures(serverFeatures);
    });
    await act(tick);
    await act(async () => { old.resolve(wallet("internal")); });
    await act(tick);
    expect(useUserStore.getState().user?.id).toBe("member");
    expect(latest.error).toBeNull();
    expect(latest.walletMode).toBe("external");
    expect(latest.availableMicrocredits).toBe(50_000_000);
    expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(2);
});

test.each(["relogin", "account round trip", "renewed session"] as const)("a previous session public refresh cannot change features after %s", async (transition) => {
    const old = deferred<typeof serverFeatures>(); featureResponse = () => old.promise;
    const refresh = refreshFeatureAvailability();
    const previousUser = useUserStore.getState().user;
    if (transition === "relogin") useUserStore.getState().clearSession();
    if (transition === "account round trip") useUserStore.getState().setUser(user("other"));
    useUserStore.getState().setUser(previousUser, { newSession: transition === "renewed session" });
    await act(async () => { old.resolve({ ...serverFeatures, creditsEnabled: false }); await refresh; });
    expect(useUserStore.getState().features.creditsEnabled).toBe(true);
});

test.each([
    ["unchanged settings", true],
    ["unrelated feature changes", false],
] as const)("a public refresh with %s preserves a valid pending wallet balance", async (_description, taskCenterEnabled) => {
    const pending = deferred<WalletSummary>();
    walletResponse = () => pending.promise;
    await show();
    expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(1);

    serverFeatures = { ...serverFeatures, taskCenterEnabled };
    await act(async () => { await refreshFeatureAvailability(); });
    expect(useUserStore.getState().features.taskCenterEnabled).toBe(taskCenterEnabled);
    await act(async () => { pending.resolve(wallet("internal")); });
    await act(tick);

    expect(latest.error).toBeNull();
    expect(latest.availableMicrocredits).toBe(10_000_000);
    expect(host.textContent).toContain("internal:10000000");
    expect(useUserStore.getState().features.taskCenterEnabled).toBe(taskCenterEnabled);
    expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(1);
});

test("a wallet mode learned after an unrelated public update preserves the updated feature", async () => {
    const pending = deferred<WalletSummary>();
    walletResponse = () => pending.promise;
    await show();
    serverFeatures = { ...serverFeatures, taskCenterEnabled: false };
    await act(async () => { await refreshFeatureAvailability(); });
    await act(async () => { pending.resolve(wallet("external")); });
    await act(tick);

    expect(latest.error).toBeNull();
    expect(latest.walletMode).toBe("external");
    expect(latest.availableMicrocredits).toBe(100_000_000);
    expect(useUserStore.getState().features.taskCenterEnabled).toBe(false);
    expect(useUserStore.getState().features.rechargeUrl).toBe("https://wallet.example/ai");
});

test.each([
    ["wallet mode", { walletMode: "external" as const }, "external", 100_000_000],
    ["credits enabled", { creditsEnabled: false }, "internal", null],
    ["recharge URL", { rechargeUrl: "https://wallet.example/new" }, "internal", 20_000_000],
] as const)("a public %s change replaces a pending wallet snapshot", async (_description, changed, mode, balance) => {
    const pending = deferred<WalletSummary>();
    walletResponse = () => pending.promise;
    await show();

    serverFeatures = { ...serverFeatures, ...changed };
    serverWallet = {
        ...wallet(mode),
        rechargeUrl: serverFeatures.rechargeUrl,
        account: { ...wallet(mode).account, availableMicrocredits: balance ?? 0 },
    };
    walletResponse = undefined;
    await act(async () => { await refreshFeatureAvailability(); });
    await act(tick);
    await act(async () => { pending.resolve(wallet("internal")); });
    await act(tick);

    expect(latest.error).toBeNull();
    expect(latest.walletMode).toBe(mode);
    expect(latest.availableMicrocredits).toBe(balance);
    expect(latest.rechargeUrl).toBe(serverFeatures.rechargeUrl);
    expect(useUserStore.getState().features.creditsEnabled).toBe(serverFeatures.creditsEnabled);
});

test.each([
    ["wallet mode", { walletMode: "external" as const }],
    ["credits enabled", { creditsEnabled: false }],
    ["recharge URL", { rechargeUrl: "https://wallet.example/new" }],
] as const)("a silent public %s change still rejects an obsolete wallet response", async (_description, changed) => {
    const pending = deferred<WalletSummary>();
    walletResponse = () => pending.promise;
    await show();

    serverFeatures = { ...serverFeatures, ...changed };
    await act(async () => { await refreshFeatureAvailability(undefined, { notifyWallet: false }); });
    await act(async () => { pending.resolve(wallet("internal")); });
    await act(tick);

    expect(latest.error?.name).toBe("AbortError");
    expect(latest.availableMicrocredits).toBeNull();
    expect(useUserStore.getState().features.walletMode).toBe(serverFeatures.walletMode);
    expect(useUserStore.getState().features.creditsEnabled).toBe(serverFeatures.creditsEnabled);
    expect(useUserStore.getState().features.rechargeUrl).toBe(serverFeatures.rechargeUrl);
});

test("wallet API mode changes update real model-picker server quoting and settle back to internal pricing", async () => {
    await show();
    expect(host.textContent).toContain("internal:10000000");
    expect(host.textContent).toContain("0.2");
    // The wallet endpoint can observe a configuration change after /features was read.
    serverWallet = wallet("external");
    await event("wallet:updated");
    expect(latest.walletMode).toBe("external");
    expect(useUserStore.getState().features.walletMode).toBe("external");
    expect(host.querySelector('[aria-label="本次消耗 1 积分"]')).not.toBeNull();
    expect(calls.filter((call) => call.url === "/model-catalog/quote")).toHaveLength(1);
    setServerMode("internal");
    await event("wallet:updated");
    expect(latest.walletMode).toBe("internal");
    expect(useUserStore.getState().features.walletMode).toBe("internal");
    expect(host.textContent).toContain("0.2");
    expect(host.querySelector('[aria-label="本次消耗 1 积分"]')).toBeNull();
});

test("a disabled internal credits feature can discover and enable an external wallet on focus", async () => {
    setServerMode("internal", false); useUserStore.getState().setFeatures(serverFeatures);
    await show(3);
    expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(0);
    setServerMode("external");
    await event("focus");
    expect(useUserStore.getState().features.creditsEnabled).toBe(true);
    expect(latest.availableMicrocredits).toBe(100_000_000);
    expect(host.querySelector('[aria-label="本次消耗 1 积分"]')).not.toBeNull();
});

test("visibility refresh is shared by all balance consumers without recursive refreshes", async () => {
    await show(3);
    setServerMode("external");
    const before = calls.length;
    await event("visibilitychange");
    expect(latest.walletMode).toBe("external");
    expect(calls.slice(before).filter((call) => call.url === "/features")).toHaveLength(1);
    expect(calls.slice(before).filter((call) => call.url === "/wallet")).toHaveLength(1);
    await act(tick);
    expect(calls.slice(before).filter((call) => call.url === "/wallet")).toHaveLength(1);
    expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(2);
});

test("an older public-feature response cannot overwrite the mode learned from a newer wallet refresh", async () => {
    await show();
    const old = deferred<typeof serverFeatures>();
    featureResponse = () => old.promise;
    const oldRefresh = refreshFeatureAvailability();
    await act(tick);
    featureResponse = undefined;
    setServerMode("external");
    await event("wallet:updated");
    await act(async () => { old.resolve({ ...defaultFeatureAvailability, walletMode: "internal" }); await oldRefresh; });
    expect(useUserStore.getState().features.walletMode).toBe("external");
    expect(latest.walletMode).toBe("external");
    expect(host.querySelector('[aria-label="本次消耗 1 积分"]')).not.toBeNull();
});

test("a later wallet event refreshes a pending old snapshot once across all consumers", async () => {
    await show(3);
    const old = deferred<WalletSummary>(); walletResponse = () => old.promise;
    await event("wallet:updated");
    walletResponse = undefined; setServerMode("external");
    await event("wallet:updated");
    await act(async () => { old.resolve(wallet("internal")); }); await act(tick);
    expect(useUserStore.getState().features.walletMode).toBe("external");
    expect(latest.availableMicrocredits).toBe(100_000_000);
    expect(host.querySelector('[aria-label="本次消耗 1 积分"]')).not.toBeNull();
    expect(calls.filter((call) => call.url === "/features")).toHaveLength(3);
    expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(3);
});

test("a wallet event during the initial balance request still discovers the latest mode", async () => {
    const old = deferred<WalletSummary>(); walletResponse = () => old.promise;
    await show(3);
    walletResponse = undefined; setServerMode("external");
    await event("wallet:updated");
    await act(async () => { old.resolve(wallet("internal")); }); await act(tick);
    expect(useUserStore.getState().features.walletMode).toBe("external");
    expect(latest.availableMicrocredits).toBe(100_000_000);
    expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(2);
});

test("a wallet event during initial disabled-feature discovery cannot be lost", async () => {
    setServerMode("internal", false); useUserStore.getState().setFeatures(serverFeatures);
    const old = deferred<typeof serverFeatures>(); featureResponse = () => old.promise;
    await show(3);
    featureResponse = undefined; setServerMode("external");
    await event("wallet:updated");
    await act(async () => { old.resolve({ ...defaultFeatureAvailability, creditsEnabled: false, walletMode: "internal" }); }); await act(tick);
    expect(useUserStore.getState().features.walletMode).toBe("external");
    expect(useUserStore.getState().features.creditsEnabled).toBe(true);
    expect(latest.availableMicrocredits).toBe(100_000_000);
    expect(calls.filter((call) => call.url === "/features")).toHaveLength(2);
    expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(1);
});

test("removing duplicate balance consumers preserves a single active event refresh", async () => {
    await show(3);
    await show(1);
    setServerMode("external");
    await event("wallet:updated");
    expect(latest.walletMode).toBe("external");
    expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(2);
});

test("a newer public refresh completes wallet discovery after an older disabled snapshot", async () => {
    setServerMode("internal", false); useUserStore.getState().setFeatures(serverFeatures);
    const first = deferred<typeof serverFeatures>(); featureResponse = () => first.promise;
    await show(3);
    setServerMode("external");
    const second = deferred<typeof serverFeatures>(); featureResponse = () => second.promise;
    const competingRefresh = refreshFeatureAvailability();
    await act(tick);
    await act(async () => { first.resolve({ ...serverFeatures }); }); await act(tick);
    expect(latest.availableMicrocredits).toBeNull();
    featureResponse = undefined;
    await act(async () => { second.resolve({ ...serverFeatures }); await competingRefresh; }); await act(tick);
    expect(useUserStore.getState().features.walletMode).toBe("external");
    expect(latest.walletMode).toBe("external");
    expect(latest.availableMicrocredits).toBe(100_000_000);
    expect(latest.error).toBeNull();
    expect(host.querySelector('[aria-label="本次消耗 1 积分"]')).not.toBeNull();
    await act(tick);
    expect(calls.filter((call) => call.url === "/features")).toHaveLength(3);
    expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(1);
});

test("a public refresh can enable a settled disabled wallet without another user event", async () => {
    setServerMode("internal", false); useUserStore.getState().setFeatures(serverFeatures);
    await show(3);
    setServerMode("external");
    await act(async () => { await refreshFeatureAvailability(); }); await act(tick);
    expect(latest.walletMode).toBe("external");
    expect(latest.availableMicrocredits).toBe(100_000_000);
    expect(host.querySelector('[aria-label="本次消耗 1 积分"]')).not.toBeNull();
    await act(tick);
    expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(1);
});

test("a newer public refresh can disable an external wallet without retaining a balance or looping", async () => {
    setServerMode("external"); useUserStore.getState().setFeatures(serverFeatures);
    await show(3);
    const first = deferred<typeof serverFeatures>(); featureResponse = () => first.promise;
    await event("wallet:updated");
    const second = deferred<typeof serverFeatures>(); featureResponse = () => second.promise;
    const competingRefresh = refreshFeatureAvailability();
    await act(tick);
    await act(async () => { first.resolve({ ...serverFeatures }); }); await act(tick);
    setServerMode("internal", false); featureResponse = undefined;
    const walletRequests = calls.filter((call) => call.url === "/wallet").length;
    await act(async () => { second.resolve({ ...serverFeatures }); await competingRefresh; }); await act(tick);
    expect(useUserStore.getState().features.creditsEnabled).toBe(false);
    expect(latest.walletMode).toBe("internal");
    expect(latest.availableMicrocredits).toBeNull();
    expect(host.querySelector('[aria-label="本次消耗 1 积分"]')).toBeNull();
    await act(tick);
    expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(walletRequests);
    expect(calls.filter((call) => call.url === "/features").length).toBeLessThanOrEqual(4);
});

test("remounting wallet consumers refreshes a cached disabled wallet changed while no listener existed", async () => {
    setServerMode("internal", false); useUserStore.getState().setFeatures(serverFeatures);
    await show(3);
    await show(0);
    setServerMode("external");
    await act(async () => { await refreshFeatureAvailability(); }); await act(tick);
    expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(0);
    await show(3);
    expect(latest.walletMode).toBe("external");
    expect(latest.availableMicrocredits).toBe(100_000_000);
    expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(1);
    expect(calls.filter((call) => call.url === "/features")).toHaveLength(3);
});

test("wallet-owned feature discovery does not broadcast an event that restarts itself", async () => {
    setServerMode("internal", false); useUserStore.getState().setFeatures(serverFeatures);
    setServerMode("external");
    let updates = 0;
    const updated = () => { updates += 1; };
    window.addEventListener("wallet:updated", updated);
    try {
        await show(3); await act(tick);
        expect(latest.availableMicrocredits).toBe(100_000_000);
        expect(updates).toBe(0);
        expect(calls.filter((call) => call.url === "/features")).toHaveLength(1);
        expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(1);
    } finally {
        window.removeEventListener("wallet:updated", updated);
    }
});

test("an old user's wallet and feature requests cannot mutate the next user's session", async () => {
    await show();
    const old = deferred<WalletSummary>(); walletResponse = () => old.promise;
    await event("wallet:updated");
    walletResponse = undefined; serverWallet = wallet("internal", "other");
    await act(async () => { useUserStore.getState().setUser(user("other")); useUserStore.getState().setFeatures(serverFeatures); });
    await act(tick);
    await act(async () => { old.resolve(wallet("external")); }); await act(tick);
    expect(useUserStore.getState().user?.id).toBe("other");
    expect(useUserStore.getState().features.walletMode).toBe("internal");
    expect(latest.walletMode).toBe("internal");
    expect(latest.availableMicrocredits).toBe(10_000_000);
});

test("an old user's public-feature response cannot enable or change the next user's wallet", async () => {
    await show();
    const old = deferred<typeof serverFeatures>();
    featureResponse = () => old.promise;
    const oldRefresh = refreshFeatureAvailability();
    await act(tick);
    featureResponse = undefined; serverWallet = wallet("internal", "other");
    await act(async () => { useUserStore.getState().setUser(user("other")); useUserStore.getState().setFeatures(serverFeatures); });
    await act(tick);
    await act(async () => { old.resolve({ ...defaultFeatureAvailability, walletMode: "external" }); await oldRefresh; });
    expect(useUserStore.getState().user?.id).toBe("other");
    expect(useUserStore.getState().features.walletMode).toBe("internal");
    expect(latest.walletMode).toBe("internal");
});

test("a model quote belongs to the user who requested it even when both users use external billing", async () => {
    setServerMode("external"); useUserStore.getState().setFeatures(serverFeatures);
    const old = deferred<number>(); quoteResponse = () => old.promise;
    await show();
    quoteResponse = async () => 2_000_000;
    serverWallet = wallet("external", "other");
    await act(async () => { useUserStore.getState().setUser(user("other")); });
    await act(tick);
    await act(async () => { old.resolve(1_000_000); }); await act(tick);
    expect(host.querySelector('[aria-label="本次消耗 2 积分"]')).not.toBeNull();
    expect(host.querySelector('[aria-label="本次消耗 1 积分"]')).toBeNull();
});

test("wallet failure keeps the newly confirmed external quote policy and hides the old balance", async () => {
    await show(); setServerMode("external"); failWallet = true;
    await event("wallet:updated");
    expect(useUserStore.getState().features.walletMode).toBe("external");
    expect(latest.walletMode).toBe("external");
    expect(latest.availableMicrocredits).toBeNull();
    expect(latest.error).not.toBeNull();
    expect(host.querySelector('[aria-label="本次消耗 1 积分"]')).not.toBeNull();
    expect(warnings.length).toBeGreaterThan(0);
});

test("failed feature discovery preserves the last confirmed policy and reports unavailable balance", async () => {
    setServerMode("external"); useUserStore.getState().setFeatures(serverFeatures);
    await show(); failFeatures = true;
    await event("wallet:updated");
    expect(useUserStore.getState().features.walletMode).toBe("external");
    expect(latest.availableMicrocredits).toBeNull();
    expect(latest.error).not.toBeNull();
});

test("returning to disabled internal credits clears the external balance and quote", async () => {
    setServerMode("external"); useUserStore.getState().setFeatures(serverFeatures);
    await show(); setServerMode("internal", false);
    const previousCalls = calls.filter((call) => call.url === "/wallet").length;
    await event("focus");
    expect(useUserStore.getState().features.creditsEnabled).toBe(false);
    expect(latest.walletMode).toBe("internal");
    expect(latest.availableMicrocredits).toBeNull();
    expect(host.querySelector('[aria-label="本次消耗 1 积分"]')).toBeNull();
    expect(calls.filter((call) => call.url === "/wallet")).toHaveLength(previousCalls);
});

test("refreshing the wallet dialog history also updates the shared balance and model pricing policy", async () => {
    await show(1, true);
    const history = [...document.querySelectorAll("button")].find((button) => button.textContent?.includes("积分消耗历史"))!;
    await act(async () => history.click());
    setServerMode("external");
    await act(async () => document.querySelector<HTMLButtonElement>(".workspace-wallet-history-toolbar button")!.click());
    await act(tick); await act(tick);
    expect(useUserStore.getState().features.walletMode).toBe("external");
    expect(latest.availableMicrocredits).toBe(100_000_000);
    expect(host.querySelector('[aria-label="本次消耗 1 积分"]')).not.toBeNull();
});
