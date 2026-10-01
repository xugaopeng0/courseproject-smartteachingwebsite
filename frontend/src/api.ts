export type User = {
  id: string;
  username: string;
  name: string;
  role: "admin" | "teacher" | "student";
};
export type Classroom = {
  id: string;
  name: string;
  description: string;
  teacher: string;
  owner_id: string;
  invite_code?: string;
  archived: boolean;
  created_at: string;
  member_count: number;
  handout_count: number;
};
export type Handout = {
  id: string;
  class_id: string;
  title: string;
  filename: string;
  size: number;
  mime: string;
  uploaded_at: string;
};
export type Detail = {
  classroom: Classroom;
  members: User[];
  handouts: Handout[];
};
export type KnowledgeLocator = {
  type: "page" | "slide" | "line" | "paragraph";
  start: number;
  end: number;
  label: string;
  converted?: boolean;
};
export type KnowledgeDocument = {
  handout_id: string;
  title: string;
  filename: string;
  status:
    | "not_indexed"
    | "queued"
    | "extracting"
    | "embedding"
    | "ready"
    | "failed"
    | "unsupported"
    | "paused";
  error_code: string;
  has_ready_index: boolean;
  strategy?: string;
  requested_strategy?: string;
  chunk_note?: string;
  updated_at?: string;
};
export type KnowledgeHit = {
  chunk_id: string;
  handout_id: string;
  title: string;
  filename: string;
  content: string;
  content_version: string;
  locator: KnowledgeLocator;
  ordinal: number;
  char_start: number;
  char_end: number;
  /** 仅向量模式返回：余弦相似度，表示相似程度，不是事实正确概率。 */
  similarity?: number;
};
export type RetrievalMode = "keyword" | "vector" | "hybrid";
export type KnowledgeSearch = {
  status: "ok" | "no_ready_documents" | "no_matches";
  mode: RetrievalMode;
  message?: string;
  ready_documents: number;
  total_documents: number;
  partial?: boolean;
  hits: KnowledgeHit[];
};
export type KnowledgeSource = Omit<KnowledgeHit, "similarity"> & {
  download_url: string;
};
export type AnswerCitation = {
  index: number;
  chunk_id: string;
  handout_id: string;
  title: string;
  filename: string;
  ordinal: number;
  char_start: number;
  char_end: number;
  content: string;
  locator: KnowledgeLocator;
  download_url: string;
};
export type KnowledgeAnswer = {
  status: "ok" | "no_matches" | "no_ready_documents";
  mode: RetrievalMode;
  answer: string;
  citations: AnswerCitation[];
  ready_documents: number;
  total_documents: number;
  partial?: boolean;
};
export class APIError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}
export async function api<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const response = await fetch("/api" + path, {
    ...options,
    credentials: "same-origin",
    headers:
      options.body instanceof FormData
        ? options.headers
        : { "Content-Type": "application/json", ...options.headers },
  });
  const body = await response.json();
  if (!response.ok) {
    if (response.status === 401 && !path.startsWith("/auth/"))
      window.dispatchEvent(new Event("session-expired"));
    throw new APIError(body.error || "请求失败，请稍后再试", response.status);
  }
  return body as T;
}
export const json = (method: string, body?: unknown): RequestInit => ({
  method,
  ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
});
export const fileURL = (h: Handout, preview = false) =>
  `/api/handouts/${h.id}/file${preview ? "?preview=1" : ""}`;
export const date = (s: string) =>
  new Date(s).toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" });
export const size = (n: number) =>
  n < 1024 * 1024
    ? `${Math.max(1, Math.round(n / 1024))} KB`
    : `${(n / 1024 / 1024).toFixed(1)} MB`;
