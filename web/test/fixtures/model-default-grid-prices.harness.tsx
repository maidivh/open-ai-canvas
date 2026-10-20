import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { Window } from "happy-dom";
import type { ModelChannel } from "../../src/stores/use-config-store";

const dom = new Window({ url: "http://localhost/" });
const originals = new Map<string, PropertyDescriptor | undefined>();
for (const [key, value] of Object.entries({ window: dom, document: dom.document, navigator: dom.navigator, localStorage: dom.localStorage, HTMLElement: dom.HTMLElement, Element: dom.Element, Node: dom.Node, SVGElement: dom.SVGElement, getComputedStyle: dom.getComputedStyle.bind(dom), IS_REACT_ACT_ENVIRONMENT: true })) {
    originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { ModelDefaultGrid } = await import("../../src/pages/settings/model-default-grid");
const { createModelChannel, defaultConfig, normalizeConfigSnapshot } = await import("../../src/stores/use-config-store");
const { defaultFeatureAvailability, useUserStore } = await import("../../src/stores/use-user-store");

const costs: NonNullable<ModelChannel["modelCosts"]> = [
    { model: "image-fixed", capability: "image", billingMode: "fixed_request", unitPriceMicrocredits: 200_000 },
    { model: "video-seconds", capability: "video", billingMode: "per_second", unitPriceMicrocredits: 50_000 },
    { model: "text-token", capability: "text", billingMode: "token", unitPriceMicrocredits: 0, inputTokenPriceMicrocredits: 100_000, outputTokenPriceMicrocredits: 300_000, cachedTokenPriceMicrocredits: 50_000 },
];
const channels = (["system", "user"] as const).map((scope) => createModelChannel({
    id: scope,
    name: scope,
    scope,
    models: costs.map((cost) => cost.model),
    modelCosts: costs.map((cost) => ({ ...cost, displayName: `${scope}-${cost.model}` })),
}));
const config = normalizeConfigSnapshot({ config: { ...defaultConfig, channels } }).config;
let root: ReturnType<typeof createRoot>;
let host: HTMLDivElement;

beforeEach(() => {
    useUserStore.getState().setFeatures({ ...defaultFeatureAvailability, walletMode: "internal" });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
});
afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    useUserStore.getState().setFeatures(defaultFeatureAvailability);
});
afterAll(() => {
    dom.happyDOM.abort();
    for (const [key, descriptor] of originals) {
        if (descriptor) Object.defineProperty(globalThis, key, descriptor);
        else Reflect.deleteProperty(globalThis, key);
    }
});

async function show(walletMode: "internal" | "external") {
    useUserStore.getState().setFeatures({ ...defaultFeatureAvailability, walletMode });
    await act(async () => root.render(<ModelDefaultGrid config={config} onChange={() => {}} />));
}
function price(scope: "system" | "user", model: string) {
    const option = [...host.querySelectorAll('[role="radio"]')].find((node) => node.textContent?.includes(`${scope}-${model}`));
    expect(option).toBeDefined();
    return option!.querySelector(".model-default-price")!;
}

for (const model of ["image-fixed", "video-seconds", "text-token"]) {
    test(`external system ${model} shows settlement guidance instead of an unquoted unit price`, async () => {
        await show("external");
        const label = price("system", model);
        expect(label.textContent).toBe("按整积分结算");
        expect(label.getAttribute("title")).toContain("每笔账单分别向上取整");
        expect(label.getAttribute("title")).toContain("以服务端报价为准");
        expect(label.textContent).not.toContain("/");
    });
}

test("internal system prices and custom-channel prices retain their configured units", async () => {
    await show("internal");
    for (const scope of ["system", "user"] as const) {
        expect(price(scope, "image-fixed").textContent).toBe("0.2 /次");
        expect(price(scope, "video-seconds").textContent).toBe("0.05 /秒");
        expect(price(scope, "text-token").textContent).toBe("0.3 /百万 Token");
    }
    await act(async () => useUserStore.getState().setFeatures({ ...defaultFeatureAvailability, walletMode: "external" }));
    expect(price("user", "image-fixed").textContent).toBe("0.2 /次");
    expect(price("user", "video-seconds").textContent).toBe("0.05 /秒");
    expect(price("user", "text-token").textContent).toBe("0.3 /百万 Token");
});

test("mounted settings update system prices when the wallet mode changes", async () => {
    await show("internal");
    expect(price("system", "image-fixed").textContent).toBe("0.2 /次");
    await act(async () => useUserStore.getState().setFeatures({ ...defaultFeatureAvailability, walletMode: "external" }));
    expect(price("system", "image-fixed").textContent).toBe("按整积分结算");
    await act(async () => useUserStore.getState().setFeatures({ ...defaultFeatureAvailability, walletMode: "internal" }));
    expect(price("system", "image-fixed").textContent).toBe("0.2 /次");
});

test("disabled credits keep all price labels hidden", async () => {
    await show("external");
    await act(async () => useUserStore.getState().setFeatures({ ...defaultFeatureAvailability, walletMode: "external", creditsEnabled: false }));
    expect(host.querySelectorAll(".model-default-price")).toHaveLength(0);
    expect(host.textContent).not.toContain("按整积分结算");
});
