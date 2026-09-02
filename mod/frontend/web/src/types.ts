export interface Session { username: string; token: string }
export interface RequestOptions { signal?: AbortSignal }
export interface ApiErrorLike { name?: string; message: string; status?: number; requestId?: string }
export type ApiFailure = ApiErrorLike | null;
export type Page<T> = { items: T[]; page: number; size: number; total: number };
export type TokenPreset = 'sub-read' | 'git-clone' | 'git-write';
export interface Token { id: string; name: string; preset: string; status: string; expiresAt?: string | null; lastUsedAt?: string | null }
export interface TokenSecret { name: string; token: string }
export interface UserProfile { id?: string; username: string; displayName?: string; systemAdmin?: boolean }
export interface Team { id?: string; slug: string; displayName: string; currentUserRole?: string; permissions?: TeamPermissions }
export interface TeamPermissions { readMembers?: boolean; manageMembers?: boolean; manageOwners?: boolean; updateSettings?: boolean }
export interface TeamMember { userId: string; username?: string; displayName?: string; role: string }
export interface Invitation { id: string; username: string; role: string; status: string; teamDisplayName?: string; teamSlug?: string; inviterUsername?: string }
export interface Plugin { namespace: string; name: string; visibility: string; status: string; repositoryStatus: string; defaultVersion?: string }
export interface PluginDetail extends Plugin { cloneUrl: string }
export interface RepositoryRef { name: string }
export interface RepositoryRefs { branches: RepositoryRef[]; tags: RepositoryRef[]; defaultRef: string }
export interface TreeEntry { name: string; type: string; size?: number; lastCommit?: { message: string } }
export interface RepositoryTree { entries: TreeEntry[] }
export interface RepositoryBlob { path: string; size: number; content: string; commitSha?: string }
export interface Commit { subject: string; authorName: string; committedAt: string; sha: string }
export interface PluginVersion { tag: string; status: string; commitSha: string; publishedAt: string }
export interface User { id: string; username: string; displayName: string; status: string; systemAdmin: boolean }
export interface EditUser { id: string; username: string; displayName: string }
export type AdminAction = 'disable' | 'enable' | 'grant' | 'revoke';
export interface AdminConfirmation { kind: AdminAction; user: User }
export function isAbortError(error: unknown): boolean { return error instanceof DOMException ? error.name === 'AbortError' : typeof error === 'object' && error !== null && 'name' in error && (error as { name?: unknown }).name === 'AbortError'; }
export function asApiError(error: unknown): ApiErrorLike { if (error instanceof Error) return error as ApiErrorLike; if (typeof error === 'object' && error !== null && 'message' in error && typeof error.message === 'string') { const value = error as { message: string; status?: unknown; requestId?: unknown }; return { message: value.message, ...(typeof value.status === 'number' ? { status: value.status } : {}), ...(typeof value.requestId === 'string' ? { requestId: value.requestId } : {}) }; } return { message: 'Request failed' }; }
