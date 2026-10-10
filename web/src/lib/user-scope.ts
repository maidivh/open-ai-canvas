// [小洞免登定制] 导出原有存储键，供 AuthSessionHydrator 监听其他标签页发布的账号切换。
export const ACTIVE_USER_SCOPE_KEY = "yingce:active-user-scope";
const GUEST_SCOPE = "guest";
// [小洞免登定制] 将上游「每次读取共享账号」改为「本标签持有账号」。
// 其他标签改 localStorage 时，旧内存和延迟保存仍归属旧账号，直到本标签明确切换或重载。
let activeTabScope: string | undefined;
let userScopedPersistenceSuppressionDepth = 0;

export function withUserScopedPersistenceSuppressed<T>(operation: () => T) {
    userScopedPersistenceSuppressionDepth += 1;
    try {
        return operation();
    } finally {
        userScopedPersistenceSuppressionDepth -= 1;
    }
}

export function isUserScopedPersistenceSuppressed() {
    return userScopedPersistenceSuppressionDepth > 0;
}

export function getActiveUserScope() {
    if (typeof window === "undefined") return GUEST_SCOPE;
    // 首次读取沿用已有 scope；后续只由 setActiveUserScope 更新，防止旧草稿被写入新账号缓存。
    return activeTabScope ??= window.localStorage.getItem(ACTIVE_USER_SCOPE_KEY) || GUEST_SCOPE;
}

export function setActiveUserScope(userId?: string | null) {
    if (typeof window === "undefined") return;
    // 本标签完成会话切换时更新内存，再通知其他标签；storage 事件不会回传给写入者自己。
    activeTabScope = userId || GUEST_SCOPE;
    window.localStorage.setItem(ACTIVE_USER_SCOPE_KEY, activeTabScope);
}

/** [小洞免登定制] JSON 与模型请求共用页面账号头；它只用于防串号，认证仍由 HttpOnly Cookie 完成。 */
export function userScopeHeaders(): Record<string, string> {
    const scope = getActiveUserScope();
    return scope === GUEST_SCOPE ? {} : { "X-Canvas-User-ID": scope };
}

export function scopedStorageKey(name: string, scope = getActiveUserScope()) {
    return `${name}:user:${scope}`;
}

export const scopedLocalStorage = {
    getItem: (name: string) => {
        if (typeof window === "undefined") return null;
        return window.localStorage.getItem(scopedStorageKey(name));
    },
    setItem: (name: string, value: string) => {
        if (typeof window === "undefined") return;
        if (isUserScopedPersistenceSuppressed()) return;
        window.localStorage.setItem(scopedStorageKey(name), value);
    },
    removeItem: (name: string) => {
        if (typeof window === "undefined") return;
        if (isUserScopedPersistenceSuppressed()) return;
        window.localStorage.removeItem(scopedStorageKey(name));
    },
};
