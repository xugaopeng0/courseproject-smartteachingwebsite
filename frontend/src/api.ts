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
export type SkillState = {
  name: string;
  body: string;
  enabled: boolean;
  scope: "platform" | "class";
};
export type PromptView = {
  prompt_body: string;
  prompt_source: "class" | "platform" | "none";
  skills: SkillState[];
  can_edit: boolean;
  archived: boolean;
};
export type ChatSession = {
  id: string;
  class_id: string;
  user_id: string;
  title: string;
  message_count: number;
  mine: boolean;
  created_at: string;
  last_message_at?: string;
};
export type ChatMessage = {
  id: string;
  seq: number;
  role: "user" | "assistant";
  status: "complete" | "interrupted" | "failed";
  content: string;
  citations: AnswerCitation[];
  created_at: string;
};
export type StreamHandlers = {
  onDelta: (text: string) => void;
  onCitations: (citations: AnswerCitation[], messageId: string) => void;
  onDone: (status: string, messageId: string) => void;
  onError: (message: string) => void;
};

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

// streamChatMessage 发送一条消息并逐块读取回答：POST 需要请求体、需要中止控制，
// 因此用 fetch + ReadableStream 手工解析 SSE，而不是 EventSource。
export async function streamChatMessage(
  classID: string,
  sessionID: string,
  content: string,
  handlers: StreamHandlers,
  signal?: AbortSignal,
): Promise<void> {
  const response = await fetch(
    `/api/classes/${classID}/sessions/${sessionID}/messages`,
    {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ content }),
      signal,
    },
  );
  if (!response.ok || !response.body) {
    let message = "发送失败，请稍后再试";
    try {
      const payload = await response.json();
      message = payload.error || message;
    } catch {
      // 响应不是 JSON 时保留默认提示。
    }
    if (response.status === 401)
      window.dispatchEvent(new Event("session-expired"));
    throw new APIError(message, response.status);
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let event = "";
  const dispatch = (name: string, data: string) => {
    if (!data) return;
    let payload: Record<string, unknown>;
    try {
      payload = JSON.parse(data) as Record<string, unknown>;
    } catch {
      return;
    }
    if (name === "delta") handlers.onDelta(String(payload.text ?? ""));
    else if (name === "citations")
      handlers.onCitations(
        (payload.citations as AnswerCitation[]) ?? [],
        String(payload.message_id ?? ""),
      );
    else if (name === "done")
      handlers.onDone(
        String(payload.status ?? "complete"),
        String(payload.message_id ?? ""),
      );
    else if (name === "error") handlers.onError(String(payload.error ?? "回答中断"));
  };
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";
    for (const line of lines) {
      if (line.startsWith("event:")) event = line.slice(6).trim();
      else if (line.startsWith("data:")) dispatch(event, line.slice(5).trim());
      else if (line.trim() === "") event = "";
    }
  }
}

export const json = (method: string, body?: unknown): RequestInit => ({
  method,
  ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
});
export const classPrompts = (id: string) =>
  api<PromptView>(`/classes/${id}/prompts`);
export const saveClassPrompt = (id: string, body: string) =>
  api<PromptView>(`/classes/${id}/prompts`, json("PUT", { body }));
export const resetClassPrompt = (id: string) =>
  api<PromptView>(`/classes/${id}/prompts`, json("DELETE"));
export const saveClassSkill = (
  id: string,
  name: string,
  input: { body?: string; enabled?: boolean },
) =>
  api<PromptView>(
    `/classes/${id}/skills/${encodeURIComponent(name)}`,
    json("PUT", input),
  );
export const removeClassSkill = (id: string, name: string) =>
  api<PromptView>(
    `/classes/${id}/skills/${encodeURIComponent(name)}`,
    json("DELETE"),
  );
export const fileURL = (h: Handout, preview = false) =>
  `/api/handouts/${h.id}/file${preview ? "?preview=1" : ""}`;
export const date = (s: string) =>
  new Date(s).toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" });
export const size = (n: number) =>
  n < 1024 * 1024
    ? `${Math.max(1, Math.round(n / 1024))} KB`
    : `${(n / 1024 / 1024).toFixed(1)} MB`;
