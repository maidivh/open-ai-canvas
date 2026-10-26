import { describe, expect, it } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { changeAgentPanelLayout, clampAgentPanelLayout, defaultAgentPanelLayout, resizeAgentPanelLayout, restoreAgentPanelLayout } from "@/lib/canvas/agent-panel-layout";
import { agentErrorPresentation, agentSubmissionErrorTitle } from "@/lib/canvas/agent-error-presentation";
import { ApiError } from "@/services/api/request";

const viewport = { width: 1280, height: 800 };
const start = { left: 816, top: 68, width: 448, height: 720 };

describe("Agent window layout", () => {
    it("restores the saved size and position", () => {
        expect(restoreAgentPanelLayout(JSON.stringify({ layout: start, viewport }), viewport)).toEqual(start);
    });
    it("fills the available height below the canvas toolbar", () => {
        const layout = restoreAgentPanelLayout(null, { width: 1024, height: 650 });
        expect(layout.height).toBe(566);
        expect(layout.top).toBe(72);
    });
    it("resets to full available height while preserving manually saved layouts", () => {
        expect(restoreAgentPanelLayout(JSON.stringify({ layout: start, viewport }), viewport)).toEqual(start);
        expect(defaultAgentPanelLayout(viewport)).toEqual({ left: 848, top: 72, width: 420, height: 716 });
    });
    it("keeps full height and the right anchor when the viewport grows or shrinks", () => {
        const largerViewport = { width: 1920, height: 1080 };
        const expanded = resizeAgentPanelLayout(defaultAgentPanelLayout(viewport), viewport, largerViewport);
        expect(expanded).toEqual({ left: 1488, top: 72, width: 420, height: 996 });
        expect(resizeAgentPanelLayout(expanded, largerViewport, viewport)).toEqual(defaultAgentPanelLayout(viewport));
    });
    it("keeps manually positioned windows unchanged when more space becomes available", () => {
        expect(resizeAgentPanelLayout(start, viewport, { width: 1920, height: 1080 })).toEqual(start);
    });
    it("restores the full-height layout against the current viewport after reopening", () => {
        const largerViewport = { width: 1920, height: 1080 };
        const saved = JSON.stringify({ layout: defaultAgentPanelLayout(viewport), viewport });
        expect(restoreAgentPanelLayout(saved, largerViewport)).toEqual(defaultAgentPanelLayout(largerViewport));
    });
    it("preserves manually saved layouts when reopening in a larger viewport", () => {
        const saved = JSON.stringify({ layout: start, viewport });
        expect(restoreAgentPanelLayout(saved, { width: 1920, height: 1080 })).toEqual(start);
    });
    it("keeps the toolbar clear when a desktop viewport is shorter than 504px", () => {
        const shortViewport = { width: 1280, height: 480 };
        const expected = { left: 848, top: 72, width: 420, height: 396 };
        expect(defaultAgentPanelLayout(shortViewport)).toEqual(expected);
        expect(restoreAgentPanelLayout(null, shortViewport)).toEqual(expected);
        expect(resizeAgentPanelLayout(defaultAgentPanelLayout(viewport), viewport, shortViewport)).toEqual(expected);
        expect(resizeAgentPanelLayout(expected, shortViewport, viewport)).toEqual(defaultAgentPanelLayout(viewport));
    });
    it("keeps the bottom/right anchor when resizing from top/left", () => {
        const layout = changeAgentPanelLayout(start, "northwest", -120, 60, viewport);
        expect(layout).toEqual({ left: 696, top: 128, width: 568, height: 660 });
        expect(layout.left + layout.width).toBe(start.left + start.width);
        expect(layout.top + layout.height).toBe(start.top + start.height);
    });
    it("does not grow a short panel when moving it or restoring a manual resize", () => {
        const shortViewport = { width: 1280, height: 480 };
        const shortLayout = defaultAgentPanelLayout(shortViewport);
        expect(changeAgentPanelLayout(shortLayout, "move", -20, 0, shortViewport)).toEqual({ ...shortLayout, left: shortLayout.left - 20 });
        const adjusted = changeAgentPanelLayout(shortLayout, "north", 0, -2, shortViewport);
        const saved = JSON.stringify({ layout: adjusted, viewport: shortViewport });
        const restored = restoreAgentPanelLayout(saved, { width: 1920, height: 1080 });
        expect(restored.height).toBe(adjusted.height);
        expect(restored.top).toBe(adjusted.top);
    });
    it("resizes one axis at a time on an edge", () => {
        expect(changeAgentPanelLayout(start, "west", -60, 150, viewport)).toEqual({ ...start, left: 756, width: 508 });
        expect(changeAgentPanelLayout(start, "north", -60, 150, viewport)).toEqual({ ...start, top: 218, height: 570 });
    });
    it("limits resize to viewport and minimum readable size", () => {
        expect(changeAgentPanelLayout(start, "northwest", -10000, -10000, viewport)).toEqual({ left: 12, top: 12, width: 1252, height: 776 });
        const small = changeAgentPanelLayout(start, "northwest", 10000, 10000, viewport);
        expect(small.width).toBe(360);
        expect(small.height).toBe(420);
    });
    it("keeps the title bar reachable when dragging beyond the screen", () => {
        expect(changeAgentPanelLayout(start, "move", -10000, -10000, viewport)).toEqual({ ...start, left: 12, top: 12 });
        expect(changeAgentPanelLayout(start, "move", 10000, 10000, viewport)).toEqual({ ...start, left: 820, top: 68 });
    });
    it("clamps stale preferences after changing display size", () => {
        const layout = clampAgentPanelLayout(start, { width: 320, height: 300 });
        expect(layout).toEqual({ width: 296, height: 276, left: 12, top: 12 });
    });
    it("discards malformed, partial and nonnumeric stored preferences", () => {
        for (const raw of ["invalid json", "null", "{}", '{"width":"448"}', '{"layout":{"left":12,"top":12,"width":1e999,"height":720},"viewport":{"width":1280,"height":800}}', JSON.stringify({ layout: start, viewport: { width: 0, height: 800 } })]) {
            expect(restoreAgentPanelLayout(raw, viewport)).toEqual(restoreAgentPanelLayout(null, viewport));
        }
    });
});

describe("Agent submission sync preflight contract", () => {
    it("scopes submit and approval saves to the active canvas before Agent POST", () => {
        const source = readFileSync(resolve(import.meta.dir, "../src/components/canvas/canvas-cloud-agent-panel.tsx"), "utf8");
        // 提交同步当前页面绑定的画布；审批同步该轮运行自己的画布（离开画布后仍能审批写回）。无画布时都跳过。
        expect(source).toContain("if (canvasId) await prepareCanvasForCloudAction(canvasId);");
        expect(source).toContain('if (decision === "approve" && run.canvasId) {');
        expect(source).toContain("await prepareCanvasForCloudAction(run.canvasId);");
        expect(source).not.toContain("cloudNodeIds");
        expect(source).toContain("submitDisabled={otherAgentRunning");
        expect(source).toContain("重新读取记录");
        expect(source).toContain("requestSent = true;\n            const result = submission.parentRunId ? await sendAgentMessage");
        expect(source).toContain("agentSubmissionErrorTitle(cause, accepted, requestSent)");
    });

    it("reports a sync failure as unsent while keeping POST ambiguity idempotent", () => {
        expect(agentSubmissionErrorTitle(new Error("canvas sync failed"), false, false)).toBe("消息尚未发送，输入已保留");
        expect(agentSubmissionErrorTitle(new TypeError("network failed"), false, true)).toBe("未收到服务端确认；重试将核对原请求，不重复创建");
    });

    it("syncs project assets without syncing any canvas", () => {
        const source = readFileSync(resolve(import.meta.dir, "../src/services/project-asset-sync.ts"), "utf8");
        expect(source).toContain("await saveRemoteUserDataNow([]);");
        expect(source).not.toContain("await saveRemoteUserDataNow();");
    });
});

describe("Agent error semantics", () => {
    it("uses HTTP status rather than matching error wording", () => {
        expect(agentErrorPresentation(new ApiError("Not found", { status: 404 })).title).toBe("Agent 接口或资源不存在");
        expect(agentErrorPresentation(new Error("HTTP 404"))).toEqual({ title: "Agent 执行失败", text: "HTTP 404" });
    });
    it("does not falsely claim that a disabled model channel is still enabled", () => {
        const result = agentErrorPresentation(new ApiError("系统渠道不存在或已停用", { status: 404 }));
        expect(result.text).toBe("系统渠道不存在或已停用");
        expect(JSON.stringify(result)).not.toContain("没有被停用");
    });
    it("preserves authentication, quota, and model errors", () => {
        for (const status of [401, 403, 429, 500]) {
            expect(agentErrorPresentation(new ApiError("真实业务错误", { status }), "请求失败")).toEqual({ title: "请求失败", text: "真实业务错误" });
        }
    });
    it("reports unimplemented endpoints without retry promises", () => {
        expect(agentErrorPresentation(new ApiError("Not implemented", { status: 501 })).title).toBe("当前 Agent 能力尚未开放");
    });
    it("distinguishes confirmed server failures from missing responses", () => {
        expect(agentSubmissionErrorTitle(new ApiError("系统处理失败", { status: 500 }), false)).toBe("服务端已返回错误；重试将核对原请求，不重复创建");
        expect(agentSubmissionErrorTitle(new ApiError("参数错误", { status: 400 }), false)).toBe("请求已被服务端拒绝");
        expect(agentSubmissionErrorTitle(new TypeError("network failed"), false, false)).toBe("消息尚未发送，输入已保留");
        expect(agentSubmissionErrorTitle(new TypeError("network failed"), false)).toBe("未收到服务端确认；重试将核对原请求，不重复创建");
        expect(agentSubmissionErrorTitle(undefined, true)).toBe("运行已接收，但本地提交记录清理失败");
    });
});
