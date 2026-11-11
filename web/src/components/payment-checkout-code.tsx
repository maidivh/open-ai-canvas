import { QRCode } from "antd";
import { useTranslation } from "react-i18next";

import { isPaymentQrImageURL } from "@/lib/payment-checkout-code";

import "./payment-checkout-code.css";

const QR_SIZE = 208;
const QR_DARK = "#111111";
const QR_LIGHT = "#ffffff";

type PaymentCheckoutCodeProps = {
    value: string;
    /** 认证链接通常很长，调用方可以放大二维码并降级纠错，避免微信无法识别。 */
    size?: number;
    errorLevel?: "L" | "M" | "Q" | "H";
    marginSize?: number;
};

export function PaymentCheckoutCode({ value, size = QR_SIZE, errorLevel = "M", marginSize = 0 }: PaymentCheckoutCodeProps) {
    const { t } = useTranslation();
    const className = size > QR_SIZE ? "payment-checkout-code is-large" : "payment-checkout-code";
    if (isPaymentQrImageURL(value)) {
        return (
            <div className={className}>
                <img src={value} alt={t("account.payment.qrAlt")} width={size} height={size} referrerPolicy="no-referrer" />
            </div>
        );
    }
    return (
        <div className={className}>
            <QRCode value={value} size={size} bordered={false} errorLevel={errorLevel} marginSize={marginSize} color={QR_DARK} bgColor={QR_LIGHT} />
        </div>
    );
}
