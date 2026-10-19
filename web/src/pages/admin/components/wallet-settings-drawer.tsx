import { App, Alert, Button, Form, Input, InputNumber, Radio, Skeleton, Space } from "antd";
import { useEffect, useState } from "react";

import { AppDrawer } from "@/components/ui/product/app-drawer";
import { normalizeWalletSettings, validateWalletSettings } from "@/lib/wallet-mode";
import { updateAdminWalletSettings, type WalletSettings, type WalletSettingsInput } from "@/services/api/wallet";

export function WalletSettingsDrawer({ open, onClose, settings, loading, error, onReload, onSaved }: {
    open: boolean;
    onClose: () => void;
    settings?: WalletSettings;
    loading: boolean;
    error: Error | null;
    onReload: () => void;
    onSaved: (settings: WalletSettings) => void;
}) {
    const { message } = App.useApp();
    const [form] = Form.useForm<WalletSettingsInput>();
    const [saving, setSaving] = useState(false);
    const mode = Form.useWatch("mode", form);

    useEffect(() => {
        if (open && settings) form.setFieldsValue({ ...normalizeWalletSettings(settings), clientSecret: "" });
    }, [open, settings, form]);

    const save = async (values: WalletSettingsInput) => {
        if (!settings || loading || error || saving) return;
        const input = normalizeWalletSettings({ ...values, version: settings.version });
        const validation = validateWalletSettings(input, settings.secretConfigured);
        if (validation) { message.error(validation); return; }
        setSaving(true);
        try {
            const result = await updateAdminWalletSettings(input);
            form.setFieldValue("clientSecret", "");
            onSaved(result);
            message.success("扣费设置已保存");
            onClose();
        } catch (cause) {
            message.error(cause instanceof Error ? cause.message : "保存扣费设置失败");
        } finally {
            setSaving(false);
        }
    };

    return <AppDrawer title="扣费模式" open={open} width={560} onClose={saving ? undefined : onClose} closable={!saving} maskClosable={!saving} footer={<Space><Button disabled={saving} onClick={onClose}>取消</Button><Button type="primary" loading={saving} disabled={!settings || loading || Boolean(error)} onClick={() => form.submit()}>保存并校验</Button></Space>}>
        {error ? <Alert type="error" showIcon title="扣费设置读取失败" description={error.message} action={<Button onClick={onReload}>重试</Button>} /> : loading && !settings ? <Skeleton active paragraph={{ rows: 8 }} /> : <Form form={form} layout="vertical" disabled={saving || loading} onFinish={save}>
            <Form.Item name="mode" label="钱包来源" rules={[{ required: true }]}>
                <Radio.Group options={[{ value: "internal", label: "内部扣费" }, { value: "external", label: "外部扣费 · 小洞AI" }]} />
            </Form.Item>
            <Alert className="mb-5" type="info" showIcon title={mode === "external" ? "使用小洞AI会员积分" : "使用 Canvas 内部积分"} description={mode === "external" ? "充值前往小洞AI，Canvas 本地签到、注册赠分、兑换和人工调账停用。内部余额保留；切换前需要结清计费任务。" : "保留现有充值、签到、注册赠分和兑换规则。切换不会迁移或清空任何余额。"} />
            <div hidden={mode !== "external"}>
                <Form.Item name="baseUrl" label="小洞AI站点域名" extra="仅填写域名，例如 https://example.com，不包含 /api。生产环境使用 HTTPS。"><Input autoComplete="off" placeholder="https://example.com" /></Form.Item>
                <Form.Item name="clientId" label="计费客户端标识"><Input autoComplete="off" /></Form.Item>
                <Form.Item name="clientSecret" label="计费客户端密钥" extra={settings?.secretConfigured ? "已配置。留空保留原密钥；填写新值将替换。" : "使用独立计费密钥，不使用 SSO 登录密钥。"}><Input.Password autoComplete="new-password" /></Form.Item>
                <Form.Item name="siteId" label="小洞AI站点 ID"><InputNumber min={1} precision={0} className="w-full" /></Form.Item>
                <Form.Item name="issuer" label="SSO 身份签发方" extra="必须与现有小洞AI登录绑定的 issuer 一致。"><Input autoComplete="off" /></Form.Item>
                <Form.Item name="rechargeUrl" label="小洞AI充值页面" extra="用户点击充值时打开此页面。留空则展示联系管理员。"><Input autoComplete="off" placeholder="https://example.com/ai/..." /></Form.Item>
            </div>
        </Form>}
    </AppDrawer>;
}
