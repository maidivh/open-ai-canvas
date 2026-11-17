import { useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Input } from "antd";
import { CheckCircle2, Copy, ExternalLink, ImagePlus, LibraryBig, RefreshCw, UserRoundPlus, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from "react";
import { useTranslation } from "react-i18next";

import { WorkspaceState } from "@/components/layout/workspace-state";
import { PaymentCheckoutCode } from "@/components/payment-checkout-code";
import { StatusBadge } from "@/components/ui/base/badges";
import { IconButton } from "@/components/ui/base/buttons";
import { useCopyText } from "@/hooks/use-copy-text";
import { currentLocale } from "@/i18n";
import { formatArkDate, formatArkTime } from "@/lib/ark-assets";
import { cn } from "@/lib/utils";
import { uploadResourceFile, resourceFileUrl } from "@/services/api/resources";
import {
    addWantraceAssetToLibrary,
    checkWantraceVerification,
    createWantraceAsset,
    createWantraceVerification,
    getWantraceOverview,
    refreshWantraceAsset,
    wantraceOverviewQueryKey,
    type WantraceAsset,
    type WantraceGroup,
    type WantraceVerification,
} from "@/services/api/wantrace-assets";
import { ARK_IMAGE_ACCEPT, validateArkImageFiles } from "@/services/ark-asset-upload";

function assetStatusTone(status: string): "success" | "loading" | "error" | "warning" | "neutral" {
    if (status === "Active") return "success";
    if (status === "Processing") return "loading";
    if (status === "Failed") return "error";
    if (status === "Uncertain") return "warning";
    return "neutral";
}

function assetStatusLabelKey(status: string) {
    if (status === "Active") return "library.ark.asset.status.active";
    if (status === "Failed") return "library.ark.asset.status.failed";
    if (status === "Uncertain") return "library.ark.wantrace.uncertain";
    return "library.ark.asset.status.processing";
}

/**
 * 方舟素材库「海外版」：橙幻-火山方舟-海外版独立网关。
 * 真人只能通过 H5 认证建组；组内素材审核通过后返回 asset_uri，供画布视频生成引用。
 */
export function WantraceAssetsPane() {
    const { t } = useTranslation();
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const copyText = useCopyText();
    const [selectedId, setSelectedId] = useState("");
    const [session, setSession] = useState<WantraceVerification | null>(null);
    const [composerOpen, setComposerOpen] = useState(false);
    const [draftName, setDraftName] = useState("");
    const [working, setWorking] = useState<"" | "start" | "check" | "upload">("");
    const fileInput = useRef<HTMLInputElement | null>(null);

    const overviewQuery = useQuery({
        queryKey: wantraceOverviewQueryKey,
        queryFn: ({ signal }) => getWantraceOverview(signal),
    });
    const overview = overviewQuery.data;
    const groups = useMemo(() => overview?.groups || [], [overview?.groups]);
    const pendingVerifications = useMemo(() => (overview?.verifications || []).filter((item) => item.status === "pending"), [overview?.verifications]);
    const selected = groups.find((group) => group.id === selectedId);

    useEffect(() => {
        if (composerOpen || session || (selected && groups.some((group) => group.id === selected.id))) return;
        setSelectedId(groups[0]?.id || "");
    }, [composerOpen, session, selected?.id, groups.map((group) => group.id).join(",")]);

    const invalidate = useCallback(() => queryClient.invalidateQueries({ queryKey: wantraceOverviewQueryKey }), [queryClient]);

    const applyVerification = useCallback(
        async (latest: WantraceVerification) => {
            if (latest.status === "verified") {
                message.success(t("library.ark.wantrace.verified"));
                const next = await queryClient.fetchQuery({ queryKey: wantraceOverviewQueryKey, queryFn: ({ signal }) => getWantraceOverview(signal) });
                const group = next.groups.find((item) => item.id === latest.groupId) || next.groups[next.groups.length - 1];
                setSession(null);
                setComposerOpen(false);
                setSelectedId(group?.id || "");
                return;
            }
            if (latest.status === "failed" || latest.status === "expired") {
                message.warning(latest.error || t(latest.status === "expired" ? "library.ark.wantrace.expired" : "library.ark.wantrace.failed"));
                // 保留会话卡片并显示明确状态，避免链接过期后页面像“突然消失”。
                setSession((current) => ({ ...(current || latest), ...latest, h5Link: current?.h5Link || latest.h5Link }));
                void invalidate();
                return;
            }
            setSession((current) => ({ ...(current || latest), ...latest, h5Link: current?.h5Link || latest.h5Link }));
        },
        [invalidate, message, queryClient, t],
    );

    const checkQuery = useQuery({
        queryKey: ["wantrace-verification-check", session?.id],
        queryFn: () => checkWantraceVerification(session!.id),
        enabled: Boolean(session?.id && session.status === "pending"),
        refetchInterval: 4_000,
        refetchIntervalInBackground: true,
    });
    useEffect(() => {
        const latest = checkQuery.data?.verification;
        if (latest) void applyVerification(latest);
    }, [checkQuery.data, applyVerification]);

    // 审核中的素材由后端向上游刷新；页面只负责触发并回读本地状态。
    const processingKey = (selected?.assets || [])
        .filter((asset) => asset.status === "Processing")
        .map((asset) => asset.id)
        .join(",");
    useEffect(() => {
        if (!processingKey) return;
        const ids = processingKey.split(",");
        let active = true;
        const tick = async () => {
            let settled = false;
            for (const id of ids) {
                try {
                    const result = await refreshWantraceAsset(id);
                    if (result.asset.status !== "Processing") settled = true;
                } catch {
                    // 单次刷新失败不打断轮询；下一轮继续。
                }
            }
            if (settled && active) await invalidate();
        };
        const timer = window.setInterval(() => void tick(), 5_000);
        void tick();
        return () => {
            active = false;
            window.clearInterval(timer);
        };
    }, [processingKey, invalidate]);

    const start = async () => {
        const name = draftName.trim();
        if (!name) return;
        setWorking("start");
        try {
            const result = await createWantraceVerification({ name });
            setSession(result.verification);
            setComposerOpen(false);
            setDraftName("");
            void invalidate();
        } catch (error) {
            message.error(error instanceof Error ? error.message : t("library.ark.actionFailed"));
        } finally {
            setWorking("");
        }
    };

    const restart = (name: string) => {
        setDraftName(name);
        setSession(null);
        setComposerOpen(true);
    };

    const checkNow = async () => {
        if (!session) return;
        setWorking("check");
        try {
            const result = await checkWantraceVerification(session.id);
            await applyVerification(result.verification);
        } catch (error) {
            message.warning(error instanceof Error ? error.message : t("library.ark.actionFailed"));
        } finally {
            setWorking("");
        }
    };

    const uploadFiles = async (files: File[]) => {
        if (!selected || !files.length) return;
        setWorking("upload");
        try {
            const checked = await validateArkImageFiles(files);
            for (const item of checked) {
                const resource = await uploadResourceFile(item.file, "image", { fileName: item.file.name, width: item.width, height: item.height });
                await createWantraceAsset(selected.id, { resourceId: resource.id, name: item.file.name });
            }
            message.success(t("library.ark.upload.submittedToArk", { count: checked.length }));
            await invalidate();
        } catch (error) {
            message.error(error instanceof Error ? error.message : t("library.ark.actionFailed"));
        } finally {
            setWorking("");
        }
    };

    if (overviewQuery.isLoading) {
        return (
            <div className="ark-pane">
                <PaneHeader />
                <div className="ark-pane-loading" role="status">
                    {t("library.ark.loading")}
                </div>
            </div>
        );
    }
    if (overviewQuery.isError && !overview) {
        return (
            <div className="ark-pane">
                <PaneHeader />
                <WorkspaceState
                    compact
                    icon="assets"
                    title={t("library.ark.loadFailed")}
                    description={overviewQuery.error instanceof Error ? overviewQuery.error.message : undefined}
                    action={<Button onClick={() => void overviewQuery.refetch()}>{t("library.ark.retry")}</Button>}
                />
            </div>
        );
    }
    if (!overview?.enabled || !overview.realEnabled) {
        return (
            <div className="ark-pane">
                <PaneHeader />
                <WorkspaceState compact icon="assets" title={t("library.ark.wantrace.disabledTitle")} description={t("library.ark.wantrace.disabledDescription")} />
            </div>
        );
    }

    const limit = overview.config.realGroupLimit || 5;
    const pendingCount = pendingVerifications.length;
    const createDisabled = overview.usage + pendingCount >= limit;

    return (
        <div className="ark-pane">
            <PaneHeader
                action={
                    <Button icon={<RefreshCw className="size-4" />} loading={overviewQuery.isFetching} onClick={() => void overviewQuery.refetch()}>
                        {t("library.ark.refresh")}
                    </Button>
                }
            />

            <div className="ark-pane-summary" aria-live="polite">
                <StatusBadge size="sm" tone="neutral" label={t("library.ark.wantrace.usage", { used: overview.usage, limit })} />
                <StatusBadge size="sm" tone="neutral" label={t("library.ark.wantrace.authDays", { days: overview.config.realAuthorizationDays })} />
            </div>

            <div className="ark-pane-body">
                <aside className="ark-pane-list" aria-label={t("library.ark.wantrace.title")}>
                    <Button
                        type="primary"
                        block
                        icon={<UserRoundPlus className="size-4" />}
                        disabled={createDisabled}
                        title={createDisabled ? t("library.ark.create.limitReached", { limit }) : undefined}
                        onClick={() => {
                            setSession(null);
                            setSelectedId("");
                            setComposerOpen(true);
                        }}
                    >
                        {t("library.ark.wantrace.create")}
                    </Button>
                    {createDisabled ? <p className="ark-pane-list-note">{t("library.ark.create.limitReached", { limit })}</p> : null}

                    {pendingVerifications.length ? (
                        <section className="ark-pane-sessions" aria-label={t("library.ark.realFlow.pendingTitle")}>
                            <span className="ark-pane-list-heading">{t("library.ark.realFlow.pendingTitle")}</span>
                            {pendingVerifications.map((item) => (
                                <button
                                    key={item.id}
                                    type="button"
                                    className={cn("ark-pane-session-item", session?.id === item.id && "is-active")}
                                    onClick={() => {
                                        setComposerOpen(false);
                                        setSelectedId("");
                                        setSession(item);
                                    }}
                                >
                                    <span className="ark-pane-dot is-pending" aria-hidden />
                                    <span>{t("library.ark.wantrace.pending", { name: item.name })}</span>
                                </button>
                            ))}
                        </section>
                    ) : null}

                    <div className="ark-pane-groups" role="list">
                        {groups.map((group) => (
                            <GroupListItem
                                key={group.id}
                                group={group}
                                active={!composerOpen && !session && group.id === selected?.id}
                                onSelect={() => {
                                    setComposerOpen(false);
                                    setSession(null);
                                    setSelectedId(group.id);
                                }}
                            />
                        ))}
                        {!groups.length ? <p className="ark-pane-list-empty">{t("library.ark.wantrace.empty")}</p> : null}
                    </div>
                </aside>

                <section className="ark-pane-main">
                    {session ? (
                        <SessionCard
                            session={session}
                            working={working === "check"}
                            onCopy={() => copyText(session.h5Link || "", t("library.ark.wantrace.copied"))}
                            onCheck={() => void checkNow()}
                            onClose={() => setSession(null)}
                            onRestart={() => restart(session.name)}
                        />
                    ) : composerOpen ? (
                        <div className="ark-composer">
                            <header className="ark-composer-head">
                                <div>
                                    <h3>{t("library.ark.wantrace.create")}</h3>
                                    <p>{t("library.ark.kindHints.real")}</p>
                                </div>
                                <Button type="text" icon={<X className="size-4" />} aria-label={t("common.close")} onClick={() => setComposerOpen(false)} />
                            </header>
                            <div className="ark-composer-form">
                                <label className="ark-field">
                                    <span>{t("library.ark.realFlow.nameLabel")}</span>
                                    <Input autoFocus value={draftName} maxLength={40} placeholder={t("library.ark.realFlow.namePlaceholder")} onChange={(event) => setDraftName(event.target.value)} onPressEnter={() => void start()} />
                                </label>
                                <div className="ark-composer-actions">
                                    <Button onClick={() => setComposerOpen(false)}>{t("common.cancel")}</Button>
                                    <Button type="primary" icon={<UserRoundPlus className="size-4" />} loading={working === "start"} disabled={!draftName.trim()} onClick={() => void start()}>
                                        {t("library.ark.wantrace.create")}
                                    </Button>
                                </div>
                            </div>
                        </div>
                    ) : selected ? (
                        <GroupDetail group={selected} working={working === "upload"} fileInput={fileInput} onUpload={(files) => void uploadFiles(files)} onRefresh={() => void invalidate()} onCopy={(value) => copyText(value)} />
                    ) : (
                        <WorkspaceState compact icon="assets" title={t("library.ark.wantrace.empty")} description={t("library.ark.wantrace.emptyHint")} />
                    )}
                </section>
            </div>
        </div>
    );
}

function PaneHeader({ action }: { action?: ReactNode }) {
    const { t } = useTranslation();
    return (
        <div className="settings-pane-header">
            <div className="min-w-0">
                <h2>{t("library.ark.wantrace.title")}</h2>
                <p>{t("library.ark.wantrace.description")}</p>
            </div>
            {action}
        </div>
    );
}

function GroupListItem({ group, active, onSelect }: { group: WantraceGroup; active: boolean; onSelect: () => void }) {
    const { t } = useTranslation();
    return (
        <button type="button" role="listitem" aria-current={active ? "true" : undefined} className={cn("ark-pane-group", active && "is-active", group.expired && "is-unavailable")} onClick={onSelect}>
            <span className="ark-pane-group-name" title={group.name}>
                {group.name}
            </span>
            <span className="ark-pane-group-meta">
                <span>{t("library.ark.group.assetCount", { count: group.assets.filter((asset) => asset.status === "Active").length })}</span>
                {group.expiresAt ? (
                    <span className={cn("ark-pane-expiry", group.expired ? "is-expired" : "is-valid")}>{group.expired ? t("library.ark.expiry.expired") : t("library.ark.expiry.until", { date: formatArkDate(group.expiresAt, currentLocale()) })}</span>
                ) : null}
            </span>
        </button>
    );
}

function SessionCard({ session, working, onCopy, onCheck, onClose, onRestart }: { session: WantraceVerification; working: boolean; onCopy: () => void; onCheck: () => void; onClose: () => void; onRestart: () => void }) {
    const { t } = useTranslation();
    const [remaining, setRemaining] = useState(() => remainingSeconds(session.expiresAt));
    useEffect(() => {
        setRemaining(remainingSeconds(session.expiresAt));
        const timer = window.setInterval(() => setRemaining(remainingSeconds(session.expiresAt)), 1_000);
        return () => window.clearInterval(timer);
    }, [session.expiresAt]);
    const live = session.status === "pending" && remaining > 0;
    const statusTitle = session.status === "expired" ? t("library.ark.wantrace.expired") : t("library.ark.wantrace.failed");
    return (
        <div className="ark-composer" aria-live="polite">
            <header className="ark-composer-head">
                <div>
                    <h3>{live ? t("library.ark.wantrace.sessionTitle", { name: session.name }) : statusTitle}</h3>
                    <p>{live ? t("library.ark.wantrace.sessionHint") : session.error || statusTitle}</p>
                </div>
                <Button type="text" icon={<X className="size-4" />} aria-label={t("common.close")} onClick={onClose} />
            </header>
            {live ? (
                <div className="ark-session">
                    {session.h5Link ? (
                        <div className="ark-session-code">
                            <PaymentCheckoutCode value={session.h5Link} size={360} errorLevel="L" marginSize={4} />
                        </div>
                    ) : null}
                    <div className="ark-session-copy">
                        <strong>{t("library.ark.wantrace.sessionTitle", { name: session.name })}</strong>
                        <span>{t("library.ark.wantrace.sessionHint")}</span>
                        <span>{t("library.ark.wantrace.sessionExpires", { time: formatArkTime(session.expiresAt, currentLocale()) })}</span>
                        <span className={cn("ark-session-remaining", remaining <= 30 && "is-warning")}>{t("library.ark.wantrace.sessionRemaining", { time: formatCountdown(remaining) })}</span>
                        <div className="ark-composer-actions is-start">
                            {session.h5Link ? (
                                <>
                                    <Button icon={<ExternalLink className="size-4" />} href={session.h5Link} target="_blank" rel="noopener noreferrer">
                                        {t("library.ark.wantrace.open")}
                                    </Button>
                                    <Button icon={<Copy className="size-4" />} onClick={onCopy}>
                                        {t("library.ark.wantrace.copy")}
                                    </Button>
                                </>
                            ) : null}
                            <Button type="primary" icon={<CheckCircle2 className="size-4" />} loading={working} onClick={onCheck}>
                                {t("library.ark.wantrace.check")}
                            </Button>
                        </div>
                    </div>
                </div>
            ) : (
                <div className="ark-composer-form">
                    <p className="ark-pane-list-note">{session.error || t("library.ark.wantrace.expired")}</p>
                    <div className="ark-composer-actions">
                        <Button onClick={onClose}>{t("common.close")}</Button>
                        <Button type="primary" icon={<UserRoundPlus className="size-4" />} onClick={onRestart}>
                            {t("library.ark.wantrace.create")}
                        </Button>
                    </div>
                </div>
            )}
        </div>
    );
}

function remainingSeconds(expiresAt: string) {
    const timestamp = Date.parse(expiresAt);
    if (!Number.isFinite(timestamp)) return 0;
    return Math.max(0, Math.floor((timestamp - Date.now()) / 1_000));
}

function formatCountdown(seconds: number) {
    const minutes = Math.floor(seconds / 60);
    return `${String(minutes).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`;
}

function GroupDetail({
    group,
    working,
    fileInput,
    onUpload,
    onRefresh,
    onCopy,
}: {
    group: WantraceGroup;
    working: boolean;
    fileInput: RefObject<HTMLInputElement | null>;
    onUpload: (files: File[]) => void;
    onRefresh: () => void;
    onCopy: (value: string) => void;
}) {
    const { t } = useTranslation();
    const writable = !group.expired && group.state === "ready";
    return (
        <article className="ark-detail">
            <header className="ark-detail-head">
                <div className="ark-detail-title">
                    <div className="ark-detail-name">
                        <h3>{group.name}</h3>
                    </div>
                    <div className="ark-detail-tags">
                        <span className="ark-pane-tag">{t("library.ark.kinds.real")}</span>
                        {group.expiresAt ? (
                            <span className={cn("ark-pane-tag", group.expired && "is-danger")}>{group.expired ? t("library.ark.group.expired") : t("library.ark.expiry.until", { date: formatArkDate(group.expiresAt, currentLocale()) })}</span>
                        ) : null}
                    </div>
                </div>
                <Button icon={<RefreshCw className="size-4" />} onClick={onRefresh}>
                    {t("library.ark.refresh")}
                </Button>
            </header>

            <dl className="ark-detail-meta">
                <div>
                    <dt>{t("library.ark.detail.group")}</dt>
                    <dd>
                        <code title={group.remoteId}>{group.remoteId}</code>
                        <IconButton size="xs" icon={Copy} aria-label={t("library.ark.group.copyId")} onClick={() => onCopy(group.remoteId)} />
                    </dd>
                </div>
            </dl>

            {group.expired ? <p className="ark-pane-list-note">{t("library.ark.group.expired")}</p> : null}

            {writable ? (
                <section className="ark-detail-upload" aria-label={t("library.ark.group.upload")}>
                    <div className="ark-detail-upload-copy">
                        <strong>{t("library.ark.group.uploadTipReal")}</strong>
                        <span>{t("library.ark.group.requirements")}</span>
                    </div>
                    <div className="ark-detail-upload-actions">
                        <Button type="primary" icon={<ImagePlus className="size-4" />} loading={working} onClick={() => fileInput.current?.click()}>
                            {t("library.ark.group.upload")}
                        </Button>
                    </div>
                    <input
                        ref={fileInput}
                        hidden
                        multiple
                        type="file"
                        accept={ARK_IMAGE_ACCEPT}
                        onChange={(event) => {
                            const files = Array.from(event.target.files || []);
                            event.target.value = "";
                            onUpload(files);
                        }}
                    />
                </section>
            ) : null}

            {group.assets.length ? (
                <ul className="ark-tiles" aria-label={t("library.ark.group.assetCount", { count: group.assets.length })}>
                    {group.assets.map((asset) => (
                        <WantraceAssetTile key={asset.id} asset={asset} onCopy={onCopy} onChanged={onRefresh} />
                    ))}
                </ul>
            ) : (
                <p className="ark-detail-empty">{t("library.ark.group.emptyAssets")}</p>
            )}
        </article>
    );
}

function WantraceAssetTile({ asset, onCopy, onChanged }: { asset: WantraceAsset; onCopy: (value: string) => void; onChanged: () => void }) {
    const { t } = useTranslation();
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const [broken, setBroken] = useState(false);
    const [adding, setAdding] = useState(false);

    const addToLibrary = async () => {
        setAdding(true);
        try {
            await addWantraceAssetToLibrary(asset.id);
            message.success(t("library.ark.asset.addedToLibrary"));
            void queryClient.invalidateQueries({ queryKey: ["asset-library"] });
            void queryClient.invalidateQueries({ queryKey: ["asset-picker"] });
            onChanged();
        } catch (error) {
            message.error(error instanceof Error ? error.message : t("library.ark.actionFailed"));
        } finally {
            setAdding(false);
        }
    };
    return (
        <li className={cn("ark-tile", asset.status === "Failed" && "is-failed")}>
            <div className="ark-tile-media">
                {broken ? <span className="ark-tile-missing">{t("library.ark.asset.fileDeleted")}</span> : <img src={resourceFileUrl(asset.resourceId)} alt={asset.name} loading="lazy" decoding="async" draggable={false} onError={() => setBroken(true)} />}
                <StatusBadge className="ark-tile-status" size="sm" variant="filled" tone={assetStatusTone(asset.status)} label={t(assetStatusLabelKey(asset.status))} />
            </div>
            <div className="ark-tile-body">
                <span className="ark-tile-name" title={asset.name}>
                    {asset.name}
                </span>
                {asset.assetUri ? (
                    <button type="button" className="ark-tile-id" title={asset.assetUri} onClick={() => onCopy(asset.assetUri)}>
                        {asset.assetUri}
                    </button>
                ) : null}
                {asset.error ? <span className="ark-tile-reason">{asset.error}</span> : null}
            </div>
            <div className="ark-tile-actions">
                {asset.status === "Active" ? (
                    asset.libraryAssetId ? (
                        <span className="ark-tile-library">
                            <LibraryBig aria-hidden />
                            {t("library.ark.asset.inLibrary")}
                        </span>
                    ) : (
                        <Button size="small" icon={<LibraryBig className="size-3.5" />} loading={adding} onClick={() => void addToLibrary()}>
                            {t("library.ark.asset.addToLibrary")}
                        </Button>
                    )
                ) : null}
            </div>
        </li>
    );
}
