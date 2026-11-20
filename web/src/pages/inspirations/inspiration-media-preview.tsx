import { useState } from "react";

import { MediaPlaceholder } from "@/components/ui/product/media-placeholder";
import type { CreationInspiration } from "@/lib/inspirations/catalog";

export function InspirationMediaPreview({ item, showSourceLink }: { item: CreationInspiration; showSourceLink: boolean }) {
    const [failed, setFailed] = useState(false);
    const [coverFailed, setCoverFailed] = useState(false);
    const videoUrl = item.mode === "video" && /^https?:\/\//i.test(item.videoUrl ?? "") ? item.videoUrl : undefined;

    return (
        <div className="inspiration-detail-cover flex-col">
            {videoUrl && !failed ? (
                <video className="block min-h-0 max-h-full w-full object-contain" src={videoUrl} poster={item.image} controls playsInline preload="none" aria-label={`${item.title} 视频预览`} onError={() => setFailed(true)} />
            ) : coverFailed ? (
                <MediaPlaceholder failed />
            ) : (
                <img className="min-h-0" src={item.image} alt="" loading="lazy" decoding="async" referrerPolicy="no-referrer" onError={() => setCoverFailed(true)} />
            )}
            {item.mode === "video" ? (
                <p className="px-5 py-2 text-xs text-foreground/60" role={failed ? "status" : undefined}>
                    {failed
                        ? showSourceLink && item.sourceUrl
                            ? "视频暂时无法加载，可通过下方「查看上游内容」访问原案例。"
                            : "视频暂时无法加载，请稍后重新打开重试。"
                        : videoUrl
                          ? "上游案例视频，点击播放预览。"
                          : "此灵感仅提供提示词与封面，暂无视频预览。"}
                </p>
            ) : null}
        </div>
    );
}
