import { describe, expect, test } from "bun:test";
import { CONTENT_MODERATION_MESSAGE, generationErrorMessage, generationFailureMetadata } from "../src/lib/generation-error";
import { ApiError } from "../src/services/api/request";

describe("wallet failure messages", () => {
    test("saved internal-credit failures describe that request rather than the current external balance", () => {
        for (const raw of ["积分不足，请先使用兑换码充值", "积分不足，请充值后重试", "积分不足，请先充值", "模型服务价格发生变化，当前积分余额不足"]) {
            for (const input of [raw, new Error(raw), new ApiError(raw, { status: 400, reason: "invalid_argument" })]) {
                const message = generationErrorMessage(input);
                expect(message).toContain("该次请求");
                expect(message).toContain("内部积分不足");
                expect(message).toContain("当前扣费模式");
                expect(message).not.toContain("兑换码");
            }
        }
    });

    test("external insufficient funds retain the external wallet source without claiming a refund", () => {
        const message = generationErrorMessage("共享钱包拒绝操作：insufficient");
        expect(message).toContain("外部钱包");
        expect(message).toContain("积分不足");
        expect(message).toContain("订单状态");
        expect(message).not.toContain("insufficient");
        expect(message).not.toContain("已退");
    });

    test("asset quota failures preserve the actual limit instead of suggesting a recharge", () => {
        const message = generationErrorMessage(new ApiError("账号素材数量已达到 100 个上限", { status: 403, code: 40301, reason: "quota_exceeded" }));
        expect(message).toContain("素材数量");
        expect(message).toContain("100");
        expect(message).not.toContain("充值");
    });

    test("provider balance failures are not relabeled as the user's wallet balance", () => {
        const message = generationErrorMessage("模型服务余额不足，请联系管理员");
        expect(message).toBe("模型服务余额不足，请联系管理员");
        const structured = generationErrorMessage(new ApiError("积分不足，请先充值", { status: 400, reason: "provider_request_failed" }));
        expect(structured).not.toContain("内部积分");
        expect(structured).not.toContain("外部钱包");
        expect(structured).toContain("模型服务");
    });
});

describe("safe upstream error details", () => {
    const detail = "非常抱歉，生成的图片可能违反了关于裸露、色情或情色内容的防护限制。请重试或修改提示语。";
    test("drops upstream detail from the card", () => {
        const message = `模型服务拒绝了请求，请检查模型和参数；上游：${detail}`;
        expect(generationErrorMessage(new Error(message))).toBe("模型服务拒绝了请求，请检查模型和参数");
    });
    test("does not replace a safe upstream message with a generic HTTP hint", () => {
        const message = `模型服务暂时不可用（HTTP 500）；上游：${detail}`;
        expect(generationErrorMessage(message)).toBe("模型服务暂时不可用，请稍后再试。");
        expect(generationErrorMessage("模型服务暂时不可用（HTTP 500）")).toBe("模型服务暂时不可用，请稍后再试。");
    });
    test("keeps details when moderation metadata is stored", () => {
        const message = `内容审核未通过；上游：${detail}`;
        const metadata = generationFailureMetadata(message, "test prompt");
        expect(metadata.errorDetails).toBe(CONTENT_MODERATION_MESSAGE);
        expect(metadata.generationErrorCode).toBe("sensitive_words_detected");
    });
    test("gives actionable hints for known upstream failures", () => {
        expect(generationErrorMessage(`模型服务暂时不可用（HTTP 503）；上游：model_not_found request id abc`)).toContain("更换模型");
        expect(generationErrorMessage(`模型服务拒绝请求；上游：image file too large 25MB`)).toContain("压缩图片");
    });
    test("upstream marker does not bypass URL hiding", () => {
        expect(generationErrorMessage("模型服务请求失败；上游：https://private.test")).toBe("模型服务请求失败");
    });
});
