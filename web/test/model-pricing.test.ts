import { describe, expect, test } from "bun:test";

import { modelQuoteCredits, modelQuoteDescription, modelQuoteRequest, normalizeTierResolution, priceTierSummaryLabel, priceTiersForCurrentSelection, requestCreditCost } from "../src/lib/model-pricing";
import type { ModelRequirements } from "../src/lib/model-selection";
import { createModelChannel, defaultConfig, normalizeConfigSnapshot, resolveModelChannel, type AiConfig } from "../src/stores/use-config-store";
import { buildNodeConfig } from "../src/components/canvas/canvas-node-prompt-panel";
import { buildGenerationConfig } from "../src/lib/canvas/canvas-project-generation";
import { modelRequestOptions } from "../src/lib/model-selection";
import { CanvasNodeType, type CanvasNodeData } from "../src/types/canvas";
import { runBackendGenerationTaskBatch } from "../src/services/api/generation-task";
import type { CreateTaskInput, GenerationTask } from "../src/services/api/task-center";

test("external wallets wait for the server quote instead of displaying unrounded local prices", () => {
    const options = { channelMode: "remote", model: "image", modelCosts: [{ model: "image", billingMode: "fixed_request" as const, unitPriceMicrocredits: 200_000 }], count: 3 };
    expect(requestCreditCost({ ...options, walletMode: "internal" })).toBeCloseTo(0.6);
    expect(requestCreditCost({ ...options, walletMode: "external" })).toBeNull();
});

function systemConfig(input: { capability?: "image" | "video" | "text" | "audio"; logicalModelId?: string; tiers: Array<{ selector: Record<string, string>; billingMode: "fixed_request" | "per_second" | "token"; unitPriceMicrocredits: number; outputTokenPriceMicrocredits?: number }> }) {
    const capability = input.capability || "video";
    const model = capability === "video" ? "agnes-video-2.5" : `${capability}-model`;
    const channel = createModelChannel({
        id: "system-channel",
        name: "系统渠道",
        scope: "system",
        baseUrl: "/api/system-channel",
        apiKey: "system",
        apiFormat: "openai",
        models: [model],
        modelCosts: [
            {
                model,
                capability,
                pricePolicy: "channel",
                billingMode: input.tiers[0]?.billingMode || "fixed_request",
                unitPriceMicrocredits: input.tiers[0]?.unitPriceMicrocredits || 0,
                logicalModelId: input.logicalModelId,
                logicalPriceTiers: input.tiers.map((tier) => ({
                    ...tier,
                    resolution: tier.selector.vquality || "*",
                    videoSeconds: Number(tier.selector.videoSeconds || 0),
                    inputTokenPriceMicrocredits: 0,
                    outputTokenPriceMicrocredits: tier.outputTokenPriceMicrocredits ?? 0,
                    cachedTokenPriceMicrocredits: 0,
                })),
            },
        ],
    });
    return normalizeConfigSnapshot({
        config: {
            ...defaultConfig,
            channels: [channel],
            model: `${channel.id}::${model}`,
            imageModel: capability === "image" ? `${channel.id}::${model}` : defaultConfig.imageModel,
            videoModel: capability === "video" ? `${channel.id}::${model}` : defaultConfig.videoModel,
            quality: "high",
            size: "16:9",
            vquality: "720P",
            videoSeconds: "5",
            count: "3",
        },
    }).config;
}

const textVideoRequirements: ModelRequirements = {
    capability: "video",
    input: { textCount: 1, imageCount: 0, videoCount: 0, audioCount: 0, characterCount: 0 },
    videoSeconds: "5",
    options: { size: "16:9", vquality: "720", videoSeconds: 5 },
};

describe("model request pricing", () => {
    test("keeps the panel quote and generation options aligned without selecting a price tier as quality", () => {
        const config = systemConfig({
            capability: "image",
            tiers: [
                { selector: { quality: "1k" }, billingMode: "fixed_request", unitPriceMicrocredits: 1_000_000 },
                { selector: { quality: "2k" }, billingMode: "fixed_request", unitPriceMicrocredits: 2_000_000 },
                { selector: { quality: "4k" }, billingMode: "fixed_request", unitPriceMicrocredits: 4_000_000 },
            ],
        });
        for (const size of ["auto", "1024x1024", "2048x2048"]) {
            const node: CanvasNodeData = { id: "image-1", type: CanvasNodeType.Image, title: "Image", position: { x: 0, y: 0 }, width: 400, height: 400, metadata: { model: config.model, quality: "auto", size } };
            const requirements: ModelRequirements = { capability: "image", options: { quality: "auto", size } };
            const panel = buildNodeConfig(config, node, "image", requirements);
            const generation = buildGenerationConfig(config, node, "image", requirements);
            expect(modelRequestOptions(panel, "image")).toEqual(modelRequestOptions(generation, "image"));
            expect(panel.quality).toBe("auto");
        }
    });

    test("uses image dimensions to match resolution pricing when quality is automatic", () => {
        const config = systemConfig({
            capability: "image",
            tiers: [
                { selector: { quality: "1k" }, billingMode: "fixed_request", unitPriceMicrocredits: 2_000_000 },
                { selector: { quality: "2k" }, billingMode: "fixed_request", unitPriceMicrocredits: 3_000_000 },
                { selector: { quality: "4k" }, billingMode: "fixed_request", unitPriceMicrocredits: 5_000_000 },
            ],
        });
        for (const [size, price] of [["1024x1024", 2], ["2048x2048", 3], ["2880x2880", 5]] as const) {
            expect(requestCreditCost({ channelMode: "remote", modelCosts: resolveModelChannel(config, config.model).modelCosts, model: "image-model", capability: "image", config: { ...config, quality: "auto", size }, requirements: { capability: "image", options: { quality: "auto", size } }, count: 1 })).toBe(price);
        }
    });

    test("matches each image resolution and generation operation for quotes and displayed costs", () => {
        const qualities = ["1k", "2k", "4k"];
        const config = systemConfig({
            capability: "image",
            logicalModelId: "logical-image",
            tiers: ["text_to_image", "image_to_image"].flatMap((operation, operationIndex) =>
                qualities.map((quality, index) => ({
                    selector: { operation, quality },
                    billingMode: "fixed_request" as const,
                    unitPriceMicrocredits: (index + 1 + operationIndex * 3) * 1_000_000,
                })),
            ),
        });
        const channel = resolveModelChannel(config, config.model);
        for (const imageCount of [0, 1]) {
            for (const [index, quality] of qualities.entries()) {
                const requirements: ModelRequirements = {
                    capability: "image",
                    input: { textCount: 1, imageCount, characterCount: 0, videoCount: 0, audioCount: 0 },
                    options: { quality: quality.toUpperCase(), size: "16:9" },
                };
                expect(requestCreditCost({ channelMode: "remote", modelCosts: channel.modelCosts, model: "image-model", capability: "image", config, requirements, count: 1 })).toBe(index + 1 + imageCount * 3);
                expect(modelQuoteRequest(config, config.model, "image", requirements)?.intent).toMatchObject({
                    operation: imageCount ? "image_to_image" : "text_to_image",
                    inputs: { image: imageCount },
                    options: { quality: quality.toUpperCase() },
                });
            }
        }
        const characterRequirements: ModelRequirements = { capability: "image", input: { textCount: 1, imageCount: 0, characterCount: 1, videoCount: 0, audioCount: 0 }, options: { quality: "2k" } };
        expect(modelQuoteRequest(config, config.model, "image", characterRequirements)?.intent).toMatchObject({ operation: "image_to_image", inputs: { image: 1 } });
        expect(requestCreditCost({ channelMode: "remote", modelCosts: channel.modelCosts, model: "image-model", capability: "image", config, requirements: characterRequirements, count: 1 })).toBe(5);
        expect(priceTiersForCurrentSelection(channel.modelCosts![0]!.logicalPriceTiers!, "image", { ...config, quality: "8k" })).toHaveLength(0);
    });

    test("prefers a matching image specification over the uniform fallback", () => {
        const config = systemConfig({ capability: "image", tiers: [
            { selector: {}, billingMode: "fixed_request", unitPriceMicrocredits: 1_000_000 },
            { selector: { operation: "text_to_image", quality: "2k" }, billingMode: "fixed_request", unitPriceMicrocredits: 2_000_000 },
        ] });
        const tiers = resolveModelChannel(config, config.model).modelCosts![0]!.logicalPriceTiers!;
        expect(priceTiersForCurrentSelection(tiers, "image", { ...config, quality: "2k" })[0]?.unitPriceMicrocredits).toBe(2_000_000);
        expect(priceTiersForCurrentSelection(tiers, "image", { ...config, quality: "4k" })[0]?.unitPriceMicrocredits).toBe(1_000_000);
    });

    test("匹配独立图片画质与像素分辨率，并兼容存量 2K 价格档", () => {
        const config = systemConfig({
            capability: "image",
            tiers: [
                { selector: { quality: "2k" }, billingMode: "fixed_request", unitPriceMicrocredits: 2_000_000 },
                { selector: { quality: "medium", resolution: "2k" }, billingMode: "fixed_request", unitPriceMicrocredits: 3_000_000 },
            ],
        });
        const requirements: ModelRequirements = { capability: "image", options: { quality: "medium", size: "2048x2048" } };
        expect(requestCreditCost({ channelMode: "remote", modelCosts: resolveModelChannel(config, config.model).modelCosts, model: "image-model", capability: "image", config, requirements, count: 1 })).toBe(3);
        expect(priceTiersForCurrentSelection(resolveModelChannel(config, config.model).modelCosts![0]!.logicalPriceTiers!, "image", config, requirements)[0]?.unitPriceMicrocredits).toBe(3_000_000);
    });
    test("preserves provider-specific resolution enums when matching price tiers", () => {
        expect(normalizeTierResolution("768P竖")).toBe("768p竖");
        expect(normalizeTierResolution("HD_Portrait")).toBe("hd_portrait");
    });

    test("matches the current Agnes resolution and duration tier and totals per-second credits", () => {
        const config = systemConfig({
            tiers: [
                { selector: { operation: "text_to_video", vquality: "720p", videoSeconds: "5" }, billingMode: "per_second", unitPriceMicrocredits: 25_000 },
                { selector: { operation: "text_to_video", vquality: "960p", videoSeconds: "5" }, billingMode: "per_second", unitPriceMicrocredits: 40_000 },
                { selector: { operation: "image_to_video", vquality: "720p", videoSeconds: "5", imageCount: "1" }, billingMode: "per_second", unitPriceMicrocredits: 30_000 },
            ],
        });
        const channel = resolveModelChannel(config, config.model);

        expect(
            requestCreditCost({
                channelMode: "remote",
                modelCosts: channel.modelCosts,
                model: "agnes-video-2.5",
                capability: "video",
                config,
                requirements: textVideoRequirements,
                seconds: "5",
            }),
        ).toBe(0.125);
    });

    test("displays zero-price system tiers as configured free pricing", () => {
        const fixedConfig = systemConfig({
            capability: "image",
            tiers: [{ selector: {}, billingMode: "fixed_request", unitPriceMicrocredits: 0 }],
        });
        const perSecondConfig = systemConfig({
            tiers: [{ selector: {}, billingMode: "per_second", unitPriceMicrocredits: 0 }],
        });

        expect(priceTierSummaryLabel(resolveModelChannel(fixedConfig, fixedConfig.model).modelCosts![0]!.logicalPriceTiers!)).toBe("0 积分");
        expect(priceTierSummaryLabel(resolveModelChannel(perSecondConfig, perSecondConfig.model).modelCosts![0]!.logicalPriceTiers!)).toBe("0 积分/秒");
    });

    test("uses reference count and operation to avoid a text-video price tier", () => {
        const config = systemConfig({
            tiers: [
                { selector: { operation: "text_to_video", vquality: "720p", videoSeconds: "5" }, billingMode: "per_second", unitPriceMicrocredits: 25_000 },
                { selector: { operation: "image_to_video", vquality: "720p", videoSeconds: "5", imageCount: "1" }, billingMode: "per_second", unitPriceMicrocredits: 30_000 },
            ],
        });
        const requirements: ModelRequirements = {
            ...textVideoRequirements,
            input: { ...textVideoRequirements.input!, imageCount: 1 },
        };
        const matched = priceTiersForCurrentSelection(resolveModelChannel(config, config.model).modelCosts![0]!.logicalPriceTiers!, "video", config, requirements);

        expect(matched).toHaveLength(1);
        expect(matched[0]?.unitPriceMicrocredits).toBe(30_000);
    });

    test("multiplies fixed image request pricing by output count", () => {
        const config = systemConfig({
            capability: "image",
            tiers: [{ selector: { quality: "high", size: "16:9" }, billingMode: "fixed_request", unitPriceMicrocredits: 10_000 }],
        });
        const channel = resolveModelChannel(config, config.model);

        expect(
            requestCreditCost({
                channelMode: "remote",
                modelCosts: channel.modelCosts,
                model: "image-model",
                capability: "image",
                config,
                requirements: { capability: "image" },
                count: 3,
            }),
        ).toBe(0.03);
    });

    test("builds a logical-model quote using the normalized current request", () => {
        const config = systemConfig({
            logicalModelId: "logical-video-1",
            tiers: [{ selector: {}, billingMode: "per_second", unitPriceMicrocredits: 25_000 }],
        });
        const quote = modelQuoteRequest(config, config.model, "video", textVideoRequirements);

        expect(quote).toMatchObject({
            logicalModelID: "logical-video-1",
            intent: {
                capability: "video",
                operation: "text_to_video",
                inputs: { image: 0, video: 0, audio: 0 },
                options: { vquality: "720", videoSeconds: 5 },
            },
        });
    });

    test("matches video audio, resolution and duration from actual request overrides", () => {
        const config = systemConfig({ tiers: [
            { selector: {}, billingMode: "token", unitPriceMicrocredits: 0, outputTokenPriceMicrocredits: 1_000_000 },
            { selector: { vquality: "1080p", videoSeconds: "10", videoGenerateAudio: "false" }, billingMode: "token", unitPriceMicrocredits: 0, outputTokenPriceMicrocredits: 2_000_000 },
            { selector: { vquality: "1080p", videoSeconds: "10", videoGenerateAudio: "true" }, billingMode: "token", unitPriceMicrocredits: 0, outputTokenPriceMicrocredits: 3_000_000 },
        ] });
        const tiers = resolveModelChannel(config, config.model).modelCosts![0]!.logicalPriceTiers!;
        for (const videoGenerateAudio of [false, "false", true, "true"]) {
            const requirements = { ...textVideoRequirements, videoSeconds: "10", options: { vquality: "1080P", videoSeconds: 5, videoGenerateAudio } };
            const matched = priceTiersForCurrentSelection(tiers, "video", config, requirements);
            expect(matched).toHaveLength(1);
            expect(matched[0]?.outputTokenPriceMicrocredits).toBe(videoGenerateAudio === true || videoGenerateAudio === "true" ? 3_000_000 : 2_000_000);
            expect(requestCreditCost({ channelMode: "remote", modelCosts: resolveModelChannel(config, config.model).modelCosts, model: "agnes-video-2.5", capability: "video", config, requirements })).toBeNull();
        }
    });

    test("shows the configured video Token rate including free and fractional rates", () => {
        const config = systemConfig({ tiers: [
            { selector: { videoGenerateAudio: "false" }, billingMode: "token", unitPriceMicrocredits: 0, outputTokenPriceMicrocredits: 0 },
            { selector: { videoGenerateAudio: "true" }, billingMode: "token", unitPriceMicrocredits: 0, outputTokenPriceMicrocredits: 1 },
        ] });
        const tiers = resolveModelChannel(config, config.model).modelCosts![0]!.logicalPriceTiers!;
        expect(priceTierSummaryLabel(tiers, "video")).toBe("0-0.000001 积分/百万视频 Token");
        expect(priceTierSummaryLabel(tiers.slice(0, 1), "video")).toBe("0 积分/百万视频 Token");
    });

    test("builds a direct system-video quote without inventing a logical model ID", () => {
        const config = systemConfig({ tiers: [{ selector: {}, billingMode: "token", unitPriceMicrocredits: 0 }] });
        const requirements = { ...textVideoRequirements, options: { videoGenerateAudio: false, vquality: "1080p" } };
        expect(modelQuoteRequest(config, config.model, "video", requirements)).toMatchObject({ channelId: "system-channel", modelKey: "agnes-video-2.5", intent: { options: { videoGenerateAudio: false, vquality: "1080p" } } });
        expect(modelQuoteRequest(config, config.model, "video", requirements)?.logicalModelID).toBeUndefined();
        expect(modelQuoteRequest(config, config.model, "image", requirements)).toBeUndefined();
    });

    test.each(["image", "text", "audio"] as const)("external direct %s models request server quotes without logical-model IDs", (capability) => {
        const config = systemConfig({ capability, tiers: [{ selector: {}, billingMode: "fixed_request", unitPriceMicrocredits: 200_000 }] });
        const requirements: ModelRequirements = { capability, options: { imageCount: 3, maxTokens: 256, duration: 8 } };
        const request = modelQuoteRequest(config, config.model, capability, requirements, "external");
        expect(request).toMatchObject({ channelId: "system-channel", modelKey: `${capability}-model`, intent: { capability, options: requirements.options } });
        expect(request?.logicalModelID).toBeUndefined();
        expect(modelQuoteRequest(config, config.model, capability, requirements, "internal")).toBeUndefined();
        expect(modelQuoteRequest(config, config.model, capability, requirements)).toBeUndefined();
    });

    test("external mode does not route custom channels through platform quote APIs", () => {
        const config = systemConfig({ capability: "image", tiers: [{ selector: {}, billingMode: "fixed_request", unitPriceMicrocredits: 200_000 }] });
        config.channels[0].scope = "user";
        expect(modelQuoteRequest(config, config.model, "image", undefined, "external")).toBeUndefined();
    });

    test("split image tasks quote one output each, while a workbench task quotes all outputs once", () => {
        const config = systemConfig({ capability: "image", tiers: [{ selector: {}, billingMode: "fixed_request", unitPriceMicrocredits: 200_000 }] });
        const requirements: ModelRequirements = { capability: "image", options: { count: 3, size: "16:9", quality: "high" } };
        const split = modelQuoteRequest(config, config.model, "image", requirements, "external", "one-task-per-output");
        expect(split?.intent.options).toMatchObject({ count: 1, size: "16:9", quality: "high" });
        const single = modelQuoteRequest(config, config.model, "image", requirements, "external");
        expect(single?.intent.options.count).toBe(3);
        expect(requirements.options?.count).toBe(3);
        expect(config.count).toBe("3");
    });

    test("batch display sums already-rounded per-task quotes instead of rounding the raw batch price", () => {
        const taskQuote = { logicalModelId: "", billingMode: "fixed_request", amountMicrocredits: 1_000_000, quantity: 1, estimated: false };
        expect(modelQuoteDescription(taskQuote, 3)).toContain("3 积分");
        expect(modelQuoteDescription(taskQuote, 3)).toContain("3 笔任务");
        expect(modelQuoteDescription(taskQuote)).toContain("1 积分");
        expect(taskQuote.amountMicrocredits).toBe(1_000_000);
        expect(modelQuoteDescription({ ...taskQuote, amountMicrocredits: 200_000 }, 3)).toContain("0.6 积分");
        expect(modelQuoteDescription({ ...taskQuote, amountMicrocredits: 0 }, 3)).toContain("0 积分");
    });

    test.each([false, true])("external token quotes describe the authorization cap and refunds (video formula: %s)", (withVideoEstimate) => {
        const quote = {
            logicalModelId: "", billingMode: "token", amountMicrocredits: 20_000_000, quantity: 1, estimated: true,
            ...(withVideoEstimate ? { videoTokenEstimate: {
                formulaTokens: 100, reservedTokens: 120, reservationMarginPercent: 20,
                outputWidth: 1280, outputHeight: 720, framesPerSecond: 24, outputSeconds: 5,
                referenceSeconds: 0, referenceDurationEstimated: false, dimensionsEstimated: false,
            } } : {}),
        };
        const external = modelQuoteDescription(quote, 3, "external");
        expect(external).toContain("3 笔任务合计预扣上限 60 积分");
        expect(external).toContain("成功后按实际用量结算并退回差额");
        expect(external).toContain("超出预扣上限时转入核账，不自动补扣");
        expect(external).not.toContain("预计消耗");
        expect(external).not.toContain("并非费用上限");
        expect(external).not.toContain("可能补扣");
        if (withVideoEstimate) expect(external).toContain("未返回用量时按火山引擎公式结算");
        const internal = modelQuoteDescription(quote, 3, "internal");
        expect(internal).toBe(modelQuoteDescription(quote, 3));
        expect(internal).toContain("3 笔任务合计预计消耗 60 积分");
        expect(internal).toContain("预估金额并非费用上限");
        expect(internal).toContain("可能补扣或退回差额");
    });

    test("three creation outputs are three separately quoted billing tasks", async () => {
        const config = systemConfig({ capability: "image", tiers: [{ selector: {}, billingMode: "fixed_request", unitPriceMicrocredits: 200_000 }] });
        const tasks: CreateTaskInput[] = [];
        const taskQuote = { logicalModelId: "", billingMode: "fixed_request", amountMicrocredits: 1_000_000, quantity: 1, estimated: false };
        const results = await runBackendGenerationTaskBatch({
            mode: "image", prompt: "three independent images", config: { ...config, count: "1" }, count: 3,
            referenceImages: [], referenceVideos: [], referenceAudios: [],
        }, {
            createId: () => "unused",
            createTask: async (input) => {
                tasks.push(input);
                return { id: `image-${tasks.length}`, type: input.type, prompt: input.prompt || "", status: "succeeded", attempts: 1, createdAt: "", updatedAt: "", resultJson: JSON.stringify({ images: [{ dataUrl: "https://example.test/image.png" }] }) } satisfies GenerationTask;
            },
            waitTask: async (_id, options) => options!.initialTask!,
        });
        expect(results.every((result) => result.status === "fulfilled")).toBe(true);
        expect(tasks).toHaveLength(3);
        for (const task of tasks) {
            expect(task.input).toMatchObject({ config: { count: "1" }, metadata: { batchCount: 3 } });
        }
        const actualBillingTotal = tasks.reduce((sum) => sum + taskQuote.amountMicrocredits, 0) / 1_000_000;
        expect(actualBillingTotal).toBe(3);
        expect(modelQuoteCredits(taskQuote, 3)).toBe(actualBillingTotal);
    });
});
