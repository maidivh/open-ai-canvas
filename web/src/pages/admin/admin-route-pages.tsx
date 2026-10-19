import { Alert, App, Button } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Settings2, UserRoundCog } from "lucide-react";
import { lazy, useState } from "react";

import { useAdminContext } from "./admin-context";
import { AdminPageFrame } from "./components/admin-shell";
import { readAnnouncementPendingReview } from "./components/admin-announcement-safety";
import { WalletSettingsDrawer } from "./components/wallet-settings-drawer";
import { getAdminWalletSettings, type WalletSettings } from "@/services/api/wallet";
import { getFeatureAvailability, invalidateAuthSessionCache } from "@/services/api/auth";
import { useUserStore } from "@/stores/use-user-store";

const AnalyticsPanel = lazy(() => import("./components/analytics-panel"));
const AdminAnnouncementsPanel = lazy(() => import("./components/admin-announcements-panel"));
const AdminBannerAnnouncementsPanel = lazy(() => import("./components/admin-banner-announcements-panel"));
const CreditOperationsPanel = lazy(() => import("./components/credit-operations-panel"));
const AccessSettingsPanel = lazy(() => import("./components/access-settings-panel"));
const EmailSettingsPanel = lazy(() => import("./components/email-settings-panel"));
const FeatureAvailabilityPanel = lazy(() => import("./components/feature-availability-panel"));
const StorageResourcesPanel = lazy(() => import("./components/storage-resources-panel"));
const AgentLessonsPanel = lazy(() => import("./components/agent-lessons-panel"));

export function AnalyticsPage() {
    const { references } = useAdminContext();
    return (
        <AdminPageFrame title="运行总览" description="请求质量、任务队列与成本 · 趋势按自然日统计" scroll>
            <AnalyticsPanel users={references.users} channels={references.channels} />
        </AdminPageFrame>
    );
}

export function AnnouncementsPage() {
    const [publishOpen, setPublishOpen] = useState(false);
    const [publishBlocked, setPublishBlocked] = useState(() => Boolean(readAnnouncementPendingReview()));
    const [publishReturnFocus, setPublishReturnFocus] = useState<HTMLElement | null>(null);
    return (
        <AdminPageFrame
            title="系统公告"
            description="面向全体用户的通知发布与状态管理"
            actions={
                <Button
                    id="admin-announcement-publish-trigger"
                    type="primary"
                    disabled={publishBlocked}
                    title={publishBlocked ? "请先核对上一次结果不确定的发布请求" : undefined}
                    icon={<Plus className="size-4" />}
                    onClick={(event) => {
                        setPublishReturnFocus(event.currentTarget);
                        setPublishOpen(true);
                    }}
                >
                    发布公告
                </Button>
            }
        >
            <AdminAnnouncementsPanel publishOpen={publishOpen} publishBlocked={publishBlocked} publishReturnFocus={publishReturnFocus} onPublishOpenChange={setPublishOpen} onPublishBlockedChange={setPublishBlocked} />
        </AdminPageFrame>
    );
}

export function BannerAnnouncementsPage() {
    const [createOpen, setCreateOpen] = useState(false);
    return (
        <AdminPageFrame
            title="常驻通知"
            description="配置首页顶部常驻展示的通知，支持标题、状态与有效期"
            actions={
                <Button
                    type="primary"
                    icon={<Plus className="size-4" />}
                    onClick={() => setCreateOpen(true)}
                >
                    新增常驻通知
                </Button>
            }
        >
            <AdminBannerAnnouncementsPanel createOpen={createOpen} onCreateOpenChange={setCreateOpen} />
        </AdminPageFrame>
    );
}

export function CreditOperationsPage() {
    const { references } = useAdminContext();
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const settings = useQuery({ queryKey: ["admin-wallet-settings"], queryFn: getAdminWalletSettings, refetchOnWindowFocus: false });
    const [walletSettingsOpen, setWalletSettingsOpen] = useState(false);
    const [activeOperation, setActiveOperation] = useState<"policy" | "adjustment" | null>(null);
    const saved = (next: WalletSettings) => {
        queryClient.setQueryData(["admin-wallet-settings"], next);
        const currentFeatures = useUserStore.getState().features;
        useUserStore.getState().setFeatures({ ...currentFeatures, walletMode: next.mode, rechargeUrl: next.rechargeUrl, creditsEnabled: next.mode === "external" ? true : currentFeatures.creditsEnabled });
        invalidateAuthSessionCache();
        void queryClient.invalidateQueries({ queryKey: ["wallet-balance"] });
        void getFeatureAvailability().then(({ features }) => useUserStore.getState().setFeatures(features)).catch(() => message.warning("扣费设置已保存，功能状态刷新失败，请刷新页面"));
        if (next.mode === "external") setActiveOperation(null);
    };
    return (
        <AdminPageFrame
            title="积分运营"
            description="异常计费核对、积分策略与人工调账"
            actions={
                <>
                    <Button icon={<Settings2 className="size-4" />} onClick={() => setWalletSettingsOpen(true)}>
                        扣费模式{settings.data ? ` · ${settings.data.mode === "external" ? "小洞AI" : "内部"}` : ""}
                    </Button>
                    <Button icon={<Settings2 className="size-4" />} onClick={() => setActiveOperation("policy")}>
                        积分策略
                    </Button>
                    <Button type="primary" disabled={settings.isFetching || settings.isError || settings.data?.mode !== "internal"} title={settings.data?.mode === "external" ? "外部积分请在小洞AI调整" : undefined} icon={<UserRoundCog className="size-4" />} onClick={() => setActiveOperation("adjustment")}>
                        人工调账
                    </Button>
                </>
            }
        >
            {settings.isError ? <Alert type="error" showIcon title="无法确认扣费模式，人工调账暂不可用" action={<Button onClick={() => void settings.refetch()}>重试</Button>} /> : settings.data?.mode === "external" ? <Alert type="info" showIcon title="当前使用小洞AI积分" description="余额与充值在小洞AI管理。注册与签到赠分配置保留，仅内部模式生效。历史账单按原钱包结算和退款。" /> : null}
            <CreditOperationsPanel users={references.users} activeOperation={activeOperation} onOperationChange={setActiveOperation} />
            <WalletSettingsDrawer open={walletSettingsOpen} onClose={() => setWalletSettingsOpen(false)} settings={settings.data} loading={settings.isFetching} error={settings.error} onReload={() => void settings.refetch()} onSaved={saved} />
        </AdminPageFrame>
    );
}

export function AccessSettingsPage() {
    return (
        <AdminPageFrame title="登录与注册" description="先控制账号创建，再配置第三方登录入口与服务协议" scroll>
            <AccessSettingsPanel />
        </AdminPageFrame>
    );
}

export function EmailSettingsPage() {
    return (
        <AdminPageFrame title="邮件服务" description="先决定是否发送注册验证码，再配置 SMTP" scroll>
            <EmailSettingsPanel />
        </AdminPageFrame>
    );
}

export function FeatureAvailabilityPage() {
    return (
        <AdminPageFrame title="功能开放" description="按用户使用路径控制工作台、插件与模型能力" scroll>
            <FeatureAvailabilityPanel />
        </AdminPageFrame>
    );
}

export function StorageResourcesPage() {
    return (
        <AdminPageFrame title="存储资源" description="只读查看资源记录、容量分布与文件预览" scroll>
            <StorageResourcesPanel />
        </AdminPageFrame>
    );
}

export function AgentLessonsPage() {
    return (
        <AdminPageFrame title="Agent 记忆" description="按用户查看个人记忆；批准仍由用户自己处理" scroll>
            <AgentLessonsPanel />
        </AdminPageFrame>
    );
}
