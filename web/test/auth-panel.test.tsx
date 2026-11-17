import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import "../src/i18n";
import { AuthPanel } from "../src/components/auth/auth-panel";

test("the global login dialog renders its password form outside the router", () => {
    const originalWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
    Object.defineProperty(globalThis, "window", { configurable: true, value: { sessionStorage: { getItem: () => null } } });
    try {
        const html = renderToStaticMarkup(<AuthPanel variant="dialog" view="login" next="/" onViewChange={() => {}} onAuthenticated={() => {}} />);
        expect(html).toContain('type="password"');
    } finally {
        if (originalWindow) Object.defineProperty(globalThis, "window", originalWindow);
        else Reflect.deleteProperty(globalThis, "window");
    }
});
