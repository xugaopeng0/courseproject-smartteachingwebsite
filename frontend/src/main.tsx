import React, { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  BookOpen,
  GraduationCap,
  Users,
  Files,
  Plus,
  Search,
  ArrowUpRight,
  ArrowLeft,
  ArrowRight,
  LogOut,
  ChevronRight,
  Download,
  Eye,
  Trash2,
  X,
  UploadCloud,
  Copy,
  Archive,
  RotateCcw,
  Check,
  LayoutGrid,
  Menu,
  ShieldCheck,
  FileText,
  LoaderCircle,
  KeyRound,
  LockKeyhole,
} from "lucide-react";
import {
  api,
  json,
  fileURL,
  date,
  size,
  APIError,
  type User,
  type Classroom,
  type Handout,
  type Detail,
} from "./api";
import "./styles.css";

type Dialog = {
  kind: "create" | "join" | "teacher" | "upload" | "edit" | "confirm";
  title: string;
  action?: () => Promise<void>;
  text?: string;
};
const roles = { admin: "管理员", teacher: "教师", student: "学生" };
function Brand() {
  return (
    <div className="brand">
      <span className="brand-mark">
        <BookOpen size={24} />
      </span>
      <div>
        <strong>知序</strong>
        <small>智能教学平台</small>
      </div>
    </div>
  );
}
function Busy() {
  return (
    <div className="loading">
      <LoaderCircle className="spin" size={23} />
      正在加载…
    </div>
  );
}
function Empty({
  title,
  description,
  action,
}: {
  title: string;
  description: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="empty">
      <div className="empty-icon">
        <BookOpen size={30} />
      </div>
      <h3>{title}</h3>
      <p>{description}</p>
      {action}
    </div>
  );
}
function Modal({
  title,
  children,
  close,
  busy,
}: {
  title: string;
  children: React.ReactNode;
  close: () => void;
  busy: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const prior = document.activeElement as HTMLElement;
    ref.current?.querySelector<HTMLElement>("input,button")?.focus();
    const listener = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !busy) close();
      if (e.key === "Tab") {
        const items = Array.from(
          ref.current?.querySelectorAll<HTMLElement>(
            "button:not(:disabled),input,textarea,select,a[href]",
          ) || [],
        );
        const first = items[0],
          last = items[items.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last?.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first?.focus();
        }
      }
    };
    document.addEventListener("keydown", listener);
    return () => {
      document.removeEventListener("keydown", listener);
      prior?.focus();
    };
  }, [busy]);
  return (
    <div
      className="modal-backdrop"
      onClick={(e) => {
        if (e.target === e.currentTarget && !busy) close();
      }}
    >
      <div
        ref={ref}
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="modal-title"
      >
        <header>
          <h2 id="modal-title">{title}</h2>
          <button
            className="icon-button"
            aria-label="关闭弹窗"
            disabled={busy}
            onClick={close}
          >
            <X size={20} />
          </button>
        </header>
        {children}
      </div>
    </div>
  );
}
function Auth({ onLogin }: { onLogin: (u: User) => void }) {
  const [register, setRegister] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return (
    <main className="auth">
      <section className="auth-story">
        <Brand />
        <div className="auth-copy">
          <span className="eyebrow">让教与学，有序发生</span>
          <h1>
            每一份知识，
            <br />
            都有抵达的地方。
          </h1>
          <p>
            连接师生，组织课堂，分享所知。
            <br />
            从这里，开启一段清晰而专注的学习旅程。
          </p>
          <div className="book-art" aria-hidden="true">
            <div className="art-orbit" />
            <div className="art-book">
              <BookOpen size={116} strokeWidth={1} />
              <span>LEARN · CONNECT · GROW</span>
            </div>
            <div className="art-label">
              <Check size={16} />
              让知识连接每一个人
            </div>
          </div>
        </div>
        <span className="auth-footer">知序 · 为每一次成长而设计</span>
      </section>
      <section className="auth-form-area">
        <div className="auth-form">
          <div className="mobile-brand">
            <Brand />
          </div>
          <span className="eyebrow">欢迎来到知序</span>
          <h2>{register ? "创建学生账号" : "很高兴，再次见面"}</h2>
          <p className="muted">
            {register
              ? "加入你的班级，让学习从此有序。"
              : "登录你的账号，继续今天的教与学。"}
          </p>
          <div className="auth-tabs">
            <button
              className={!register ? "active" : ""}
              onClick={() => {
                setRegister(false);
                setError("");
              }}
            >
              账号登录
            </button>
            <button
              className={register ? "active" : ""}
              onClick={() => {
                setRegister(true);
                setError("");
              }}
            >
              学生注册
            </button>
          </div>
          <form
            key={String(register)}
            onSubmit={async (e) => {
              e.preventDefault();
              setError("");
              setBusy(true);
              const f = new FormData(e.currentTarget);
              try {
                onLogin(
                  await api<User>(
                    "/auth/" + (register ? "register" : "login"),
                    json("POST", {
                      username: f.get("username"),
                      password: f.get("password"),
                      ...(register ? { name: f.get("name") } : {}),
                    }),
                  ),
                );
              } catch (e) {
                setError((e as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            {register && (
              <label>
                姓名
                <input
                  name="name"
                  placeholder="请输入真实姓名"
                  autoComplete="name"
                  maxLength={40}
                  required
                />
              </label>
            )}
            <label>
              账号
              <input
                name="username"
                placeholder="请输入账号"
                autoComplete="username"
                minLength={3}
                maxLength={32}
                pattern="[A-Za-z0-9_]{3,32}"
                title="3–32 位字母、数字或下划线"
                required
              />
            </label>
            <label>
              密码
              <input
                name="password"
                type="password"
                placeholder={register ? "至少 8 位密码" : "请输入密码"}
                autoComplete={register ? "new-password" : "current-password"}
                minLength={register ? 8 : undefined}
                maxLength={72}
                required
              />
            </label>
            {error && (
              <p className="error" role="alert">
                {error}
              </p>
            )}
            <button className="primary wide" disabled={busy}>
              {busy ? (
                <LoaderCircle className="spin" size={18} />
              ) : (
                <>
                  {register ? "注册并进入平台" : "登录平台"}
                  <ArrowRight size={18} />
                </>
              )}
            </button>
          </form>
          <p className="auth-tip">
            <ShieldCheck size={16} />
            {register
              ? "教师账号由平台管理员创建"
              : "管理员、教师和学生使用同一入口登录"}
          </p>
        </div>
        <span className="auth-bottom">专注知识，共享成长。</span>
      </section>
    </main>
  );
}
function App() {
  const [user, setUser] = useState<User | null>(null);
  const [boot, setBoot] = useState(true);
  const [bootError, setBootError] = useState("");
  const [classes, setClasses] = useState<Classroom[]>([]);
  const [teachers, setTeachers] = useState<User[]>([]);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [allHandouts, setAllHandouts] = useState<Handout[]>([]);
  const [route, setRoute] = useState(location.hash.slice(1) || "classes");
  const [loading, setLoading] = useState(false);
  const [pageError, setPageError] = useState("");
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState("active");
  const [tab, setTab] = useState("handouts");
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState("");
  const [toast, setToast] = useState("");
  const [menu, setMenu] = useState(false);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [revision, setRevision] = useState(0);
  const revisionRef = useRef(0);
  const notify = (message: string) => setToast(message);
  const refresh = () => setRevision((v) => v + 1);
  const go = (page: string) => {
    location.hash = page;
    setSearch("");
    setTab("handouts");
    setMenu(false);
  };
  useEffect(() => {
    const change = () => setRoute(location.hash.slice(1) || "classes");
    const expired = () => {
      setUser(null);
      notify("登录已过期，请重新登录");
    };
    window.addEventListener("hashchange", change);
    window.addEventListener("session-expired", expired);
    return () => {
      window.removeEventListener("hashchange", change);
      window.removeEventListener("session-expired", expired);
    };
  }, []);
  useEffect(() => {
    api<User>("/auth/me")
      .then(setUser)
      .catch((e) => {
        if (!(e instanceof APIError && e.status === 401))
          setBootError(e.message);
      })
      .finally(() => setBoot(false));
  }, []);
  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(""), 4000);
    return () => clearTimeout(t);
  }, [toast]);
  useEffect(() => {
    if (!user) return;
    const token = ++revisionRef.current;
    setLoading(true);
    setPageError("");
    setDetail(null);
    (async () => {
      try {
        if (user.role === "admin") {
          const result = await api<User[]>("/admin/teachers");
          if (token === revisionRef.current) setTeachers(result);
        } else {
          const list = await api<Classroom[]>("/classes");
          if (token !== revisionRef.current) return;
          setClasses(list);
          if (route.startsWith("class/")) {
            const d = await api<Detail>("/classes/" + route.slice(6));
            if (token === revisionRef.current) setDetail(d);
          } else if (route === "handouts") {
            const details = await Promise.all(
              list.map((c) => api<Detail>("/classes/" + c.id)),
            );
            if (token === revisionRef.current)
              setAllHandouts(
                details
                  .flatMap((d) => d.handouts)
                  .sort((a, b) => b.uploaded_at.localeCompare(a.uploaded_at)),
              );
          }
        }
      } catch (e) {
        if (token === revisionRef.current) setPageError((e as Error).message);
      } finally {
        if (token === revisionRef.current) setLoading(false);
      }
    })();
    return () => {
      revisionRef.current++;
    };
  }, [user, route, revision]);
  const open = (d: Dialog) => {
    setFormError("");
    setSelectedFile(null);
    setDialog(d);
  };
  const confirm = (title: string, text: string, action: () => Promise<void>) =>
    open({ kind: "confirm", title, text, action });
  const submit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!dialog) return;
    setBusy(true);
    setFormError("");
    const f = new FormData(e.currentTarget);
    try {
      switch (dialog.kind) {
        case "create": {
          const c = await api<Classroom>(
            "/classes",
            json("POST", {
              name: f.get("name"),
              description: f.get("description"),
            }),
          );
          go("class/" + c.id);
          notify("班级已创建");
          break;
        }
        case "edit":
          await api(
            "/classes/" + detail!.classroom.id,
            json("PATCH", {
              name: f.get("name"),
              description: f.get("description"),
            }),
          );
          notify("班级信息已更新");
          break;
        case "join": {
          const c = await api<{ id: string }>(
            "/classes/join",
            json("POST", { code: f.get("code") }),
          );
          go("class/" + c.id);
          notify("已成功加入班级");
          break;
        }
        case "teacher":
          await api(
            "/admin/teachers",
            json("POST", {
              name: f.get("name"),
              username: f.get("username"),
              password: f.get("password"),
            }),
          );
          notify("教师账号已创建");
          break;
        case "upload": {
          if (!selectedFile) throw new Error("请选择讲义文件");
          if (selectedFile.size > 50 * 1024 * 1024)
            throw new Error("文件不能超过 50 MB");
          const upload = new FormData();
          upload.set("file", selectedFile);
          upload.set("title", String(f.get("title") || ""));
          await api("/classes/" + detail!.classroom.id + "/handouts", {
            method: "POST",
            body: upload,
          });
          notify("讲义上传成功");
          break;
        }
        case "confirm":
          await dialog.action?.();
          notify("操作已完成");
          break;
      }
      setDialog(null);
      refresh();
    } catch (e) {
      setFormError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const actionButton = () =>
    user?.role === "teacher" ? (
      <button
        className="primary"
        onClick={() => open({ kind: "create", title: "创建新班级" })}
      >
        <Plus size={18} />
        创建班级
      </button>
    ) : (
      <button
        className="primary"
        onClick={() => open({ kind: "join", title: "加入班级" })}
      >
        <Plus size={18} />
        加入班级
      </button>
    );
  const renderHandouts = (items: Handout[], global = false) => (
    <div className="materials">
      {items.length === 0 ? (
        <Empty
          title={search ? "没有找到匹配的讲义" : "知识，从第一份讲义开始"}
          description={
            search
              ? "试试其他关键词。"
              : user?.role === "teacher"
                ? "上传课堂资料，让同学们随时温习。"
                : "老师上传的讲义将展示在这里。"
          }
        />
      ) : (
        items.map((h) => (
          <div className="material-row" key={h.id}>
            <div
              className={
                "file-icon " + (h.mime === "application/pdf" ? "pdf" : "office")
              }
            >
              <FileText size={23} />
              <span>{h.filename.split(".").pop()?.toUpperCase()}</span>
            </div>
            <div className="material-info">
              <strong>{h.title}</strong>
              <span>
                {global
                  ? classes.find((c) => c.id === h.class_id)?.name + " · "
                  : ""}
                {size(h.size)}
                <i />
                上传于 {date(h.uploaded_at)}
              </span>
            </div>
            <div className="material-actions">
              {h.mime === "application/pdf" && (
                <a
                  className="icon-button"
                  href={fileURL(h, true)}
                  target="_blank"
                  rel="noreferrer"
                  aria-label={"预览 " + h.title}
                  title="预览"
                >
                  <Eye size={18} />
                </a>
              )}
              <a
                className="icon-button"
                href={fileURL(h)}
                aria-label={"下载 " + h.title}
                title="下载"
              >
                <Download size={18} />
              </a>
              {!global &&
                user?.id === detail?.classroom.owner_id &&
                !detail?.classroom.archived && (
                  <button
                    className="icon-button danger-hover"
                    aria-label={"删除 " + h.title}
                    title="删除"
                    onClick={() =>
                      confirm(
                        "删除讲义",
                        `确定删除「${h.title}」？删除后班级成员将无法访问。`,
                        async () => {
                          await api("/handouts/" + h.id, json("DELETE"));
                        },
                      )
                    }
                  >
                    <Trash2 size={17} />
                  </button>
                )}
            </div>
          </div>
        ))
      )}
    </div>
  );
  if (boot) return <Busy />;
  if (bootError)
    return (
      <Empty
        title="暂时无法连接平台"
        description={bootError}
        action={
          <button className="primary" onClick={() => location.reload()}>
            重新连接
          </button>
        }
      />
    );
  if (!user)
    return (
      <Auth
        onLogin={(u) => {
          setUser(u);
          go("classes");
        }}
      />
    );
  const totalMembers = classes.reduce((n, c) => n + c.member_count, 0),
    totalFiles = classes.reduce((n, c) => n + c.handout_count, 0);
  const active = classes.filter((c) => !c.archived);
  const filtered = classes.filter(
    (c) =>
      (filter === "all" ||
        (filter === "archived" ? c.archived : !c.archived)) &&
      (c.name + " " + c.description)
        .toLowerCase()
        .includes(search.toLowerCase()),
  );
  const owner = detail?.classroom.owner_id === user.id;
  return (
    <div className="app-shell">
      <aside className={"sidebar " + (menu ? "open" : "")}>
        <Brand />
        <div className="workspace-label">教学工作空间</div>
        <nav>
          <button
            className={route !== "handouts" ? "selected" : ""}
            onClick={() => go("classes")}
          >
            <LayoutGrid size={19} />
            {user.role === "admin" ? "教师管理" : "我的班级"}
            <span>
              {user.role === "admin" ? teachers.length : classes.length}
            </span>
          </button>
          {user.role !== "admin" && (
            <button
              className={route === "handouts" ? "selected" : ""}
              onClick={() => go("handouts")}
            >
              <Files size={19} />
              讲义资源<span>{totalFiles}</span>
            </button>
          )}
        </nav>
        <div className="sidebar-note">
          <span className="note-symbol">知</span>
          <strong>把课堂连接起来</strong>
          <p>
            让教学更有条理，
            <br />
            让成长清晰可见。
          </p>
          <div className="note-line" />
        </div>
        <div className="profile">
          <span className="avatar">{user.name.slice(0, 1)}</span>
          <div>
            <strong>{user.name}</strong>
            <small>{roles[user.role]}账号</small>
          </div>
          <button
            className="icon-button"
            aria-label="退出登录"
            title="退出登录"
            onClick={async () => {
              try {
                await api("/auth/logout", json("POST"));
                setUser(null);
                setClasses([]);
                setDetail(null);
              } catch (e) {
                notify((e as Error).message);
              }
            }}
          >
            <LogOut size={17} />
          </button>
        </div>
      </aside>
      {menu && <div className="sidebar-scrim" onClick={() => setMenu(false)} />}
      <div className="main-shell">
        <header className="topbar">
          <button
            className="icon-button mobile-menu"
            aria-label="打开菜单"
            onClick={() => setMenu(!menu)}
          >
            <Menu />
          </button>
          <div className="breadcrumb">
            工作空间
            <ChevronRight size={14} />
            <strong>
              {user.role === "admin"
                ? "教师管理"
                : detail
                  ? detail.classroom.name
                  : route === "handouts"
                    ? "讲义资源"
                    : "我的班级"}
            </strong>
          </div>
          <span className="term">
            <span />
            有序教学 · 共同成长
          </span>
        </header>
        <main className="main-content">
          {user.role === "admin" ? (
            <>
              <div className="page-heading">
                <div>
                  <span className="eyebrow">TEACHER MANAGEMENT</span>
                  <h1>
                    教师管理<span className="title-dot">.</span>
                  </h1>
                  <p>为教师创建账号，一起开启有序的教学。</p>
                </div>
                <button
                  className="primary"
                  onClick={() =>
                    open({ kind: "teacher", title: "创建教师账号" })
                  }
                >
                  <Plus size={18} />
                  创建教师账号
                </button>
              </div>
              <section className="panel">
                <div className="section-heading">
                  <h2>
                    教师账号 <span>{teachers.length}</span>
                  </h2>
                </div>
                {loading ? (
                  <Busy />
                ) : teachers.length === 0 ? (
                  <Empty
                    title="欢迎，平台管理员"
                    description="先创建教师账号，再由教师创建班级、上传讲义。"
                  />
                ) : (
                  teachers.map((t) => (
                    <div className="member-row" key={t.id}>
                      <span className="avatar">{t.name[0]}</span>
                      <div>
                        <strong>{t.name}</strong>
                        <small>@{t.username}</small>
                      </div>
                      <span className="badge">教师</span>
                    </div>
                  ))
                )}
              </section>
            </>
          ) : route.startsWith("class/") ? (
            <>
              {loading ? (
                <Busy />
              ) : (
                detail && (
                  <>
                    <button className="back-link" onClick={() => go("classes")}>
                      <ArrowLeft size={16} />
                      返回我的班级
                    </button>
                    <div className="detail-heading">
                      <div className="class-emblem">
                        <BookOpen size={33} />
                      </div>
                      <div className="detail-title">
                        <div className="inline">
                          <span className="eyebrow">CLASSROOM</span>
                          <span
                            className={
                              "badge " +
                              (detail.classroom.archived ? "gray" : "")
                            }
                          >
                            {detail.classroom.archived ? "已归档" : "进行中"}
                          </span>
                        </div>
                        <h1>{detail.classroom.name}</h1>
                        <p>
                          {detail.classroom.description ||
                            "一起探索新知，让每一次学习都有收获。"}
                        </p>
                        <div className="class-meta">
                          <span>
                            <GraduationCap size={16} />
                            {detail.classroom.teacher}
                          </span>
                          <span>
                            <Users size={16} />
                            {detail.members.length} 位同学
                          </span>
                          <span>
                            <Files size={16} />
                            {detail.handouts.length} 份讲义
                          </span>
                        </div>
                      </div>
                      {owner && !detail.classroom.archived && (
                        <button
                          className="secondary"
                          onClick={() =>
                            open({ kind: "edit", title: "编辑班级信息" })
                          }
                        >
                          编辑班级
                        </button>
                      )}
                    </div>
                    {owner && (
                      <div className="invite-bar">
                        <div>
                          <KeyRound size={18} />
                          <span>班级邀请码</span>
                          <strong>{detail.classroom.invite_code}</strong>
                        </div>
                        <button
                          className="text-button"
                          onClick={async () => {
                            try {
                              await navigator.clipboard.writeText(
                                detail.classroom.invite_code || "",
                              );
                              notify("邀请码已复制");
                            } catch {
                              notify("复制失败，请手动选择邀请码");
                            }
                          }}
                        >
                          <Copy size={15} />
                          复制邀请码
                        </button>
                        <span className="invite-hint">
                          分享给学生，即可加入班级
                        </span>
                      </div>
                    )}
                    {detail.classroom.archived && (
                      <div className="archive-banner">
                        <Archive size={17} />
                        此班级已归档，现有讲义仍可查看和下载。
                      </div>
                    )}
                    <section className="panel detail-panel">
                      <div className="detail-tabs">
                        <button
                          className={tab === "handouts" ? "active" : ""}
                          onClick={() => setTab("handouts")}
                        >
                          班级讲义 <span>{detail.handouts.length}</span>
                        </button>
                        <button
                          className={tab === "members" ? "active" : ""}
                          onClick={() => setTab("members")}
                        >
                          班级成员 <span>{detail.members.length}</span>
                        </button>
                        <button
                          className={tab === "settings" ? "active" : ""}
                          onClick={() => setTab("settings")}
                        >
                          班级设置
                        </button>
                      </div>
                      {tab === "handouts" ? (
                        <>
                          <div className="section-heading">
                            <div>
                              <h2>教学资料</h2>
                              <p>课前预习，课后温习，知识随时可达。</p>
                            </div>
                            {owner && !detail.classroom.archived && (
                              <button
                                className="primary"
                                onClick={() =>
                                  open({ kind: "upload", title: "上传讲义" })
                                }
                              >
                                <UploadCloud size={18} />
                                上传讲义
                              </button>
                            )}
                          </div>
                          {renderHandouts(detail.handouts)}
                        </>
                      ) : tab === "members" ? (
                        <>
                          <div className="section-heading">
                            <h2>任课教师与同学</h2>
                          </div>
                          <div className="member-row">
                            <span className="avatar red">
                              {detail.classroom.teacher[0]}
                            </span>
                            <div>
                              <strong>{detail.classroom.teacher}</strong>
                              <small>任课教师</small>
                            </div>
                            <span className="badge">教师</span>
                          </div>
                          {detail.members.map((m) => (
                            <div className="member-row" key={m.id}>
                              <span className="avatar">{m.name[0]}</span>
                              <div>
                                <strong>{m.name}</strong>
                                <small>
                                  {m.username ? "@" + m.username : "学生"}
                                </small>
                              </div>
                              {owner && !detail.classroom.archived && (
                                <button
                                  className="text-button danger"
                                  onClick={() =>
                                    confirm(
                                      "移除班级成员",
                                      `确定将 ${m.name} 移出班级？该同学将无法再访问班级讲义。`,
                                      async () => {
                                        await api(
                                          `/classes/${detail.classroom.id}/members/${m.id}`,
                                          json("DELETE"),
                                        );
                                      },
                                    )
                                  }
                                >
                                  移除
                                </button>
                              )}
                            </div>
                          ))}
                        </>
                      ) : (
                        <div className="settings-row">
                          <div>
                            <h3>
                              {owner
                                ? detail.classroom.archived
                                  ? "恢复班级"
                                  : "归档班级"
                                : "退出班级"}
                            </h3>
                            <p>
                              {owner
                                ? "归档后停止新增教学内容，已有资料仍可访问。"
                                : "退出后将无法访问班级讲义，可通过邀请码重新加入。"}
                            </p>
                          </div>
                          <button
                            className="secondary"
                            onClick={() =>
                              confirm(
                                owner
                                  ? detail.classroom.archived
                                    ? "恢复班级"
                                    : "归档班级"
                                  : "退出班级",
                                owner
                                  ? detail.classroom.archived
                                    ? "恢复后可继续管理成员和上传讲义。"
                                    : "归档后无法加入班级、编辑班级或上传讲义，已有资料可继续访问。"
                                  : "确定退出此班级？",
                                async () => {
                                  if (owner)
                                    await api(
                                      "/classes/" +
                                        detail.classroom.id +
                                        "/archive",
                                      json("POST", {
                                        archived: !detail.classroom.archived,
                                      }),
                                    );
                                  else {
                                    await api(
                                      `/classes/${detail.classroom.id}/members/${user.id}`,
                                      json("DELETE"),
                                    );
                                    go("classes");
                                  }
                                },
                              )
                            }
                          >
                            {owner ? (
                              detail.classroom.archived ? (
                                <RotateCcw size={16} />
                              ) : (
                                <Archive size={16} />
                              )
                            ) : (
                              <LogOut size={16} />
                            )}{" "}
                            {owner
                              ? detail.classroom.archived
                                ? "恢复班级"
                                : "归档班级"
                              : "退出班级"}
                          </button>
                        </div>
                      )}
                    </section>
                  </>
                )
              )}
            </>
          ) : route === "handouts" ? (
            <>
              <div className="page-heading">
                <div>
                  <span className="eyebrow">LEARNING RESOURCES</span>
                  <h1>
                    讲义资源<span className="title-dot">.</span>
                  </h1>
                  <p>汇集班级资料，让每一份知识触手可及。</p>
                </div>
              </div>
              <section className="panel">
                <div className="section-heading">
                  <h2>
                    全部讲义 <span>{allHandouts.length}</span>
                  </h2>
                  <div className="search">
                    <Search size={17} />
                    <input
                      aria-label="搜索讲义"
                      placeholder="搜索讲义名称"
                      value={search}
                      onChange={(e) => setSearch(e.target.value)}
                    />
                  </div>
                </div>
                {loading ? (
                  <Busy />
                ) : (
                  renderHandouts(
                    allHandouts.filter((h) =>
                      h.title.toLowerCase().includes(search.toLowerCase()),
                    ),
                    true,
                  )
                )}
              </section>
            </>
          ) : (
            <>
              <div className="page-heading">
                <div>
                  <span className="eyebrow">MY CLASSROOMS</span>
                  <h1>
                    我的班级<span className="title-dot">.</span>
                  </h1>
                  <p>
                    {user.role === "teacher"
                      ? "每一间课堂，都是知识与成长相遇的地方。"
                      : "在这里连接课堂，让每一天的学习都有收获。"}
                  </p>
                </div>
                {actionButton()}
              </div>
              <section className="welcome-banner">
                <div>
                  <span className="banner-label">你好，{user.name}</span>
                  <h2>
                    {user.role === "teacher"
                      ? "用心教学，让每一份热爱都有回响。"
                      : "保持好奇，让每一次探索都有收获。"}
                  </h2>
                  <p>
                    {user.role === "teacher"
                      ? "管理你的班级，与同学们分享新的知识。"
                      : "进入你的班级，开启今天的学习旅程。"}
                  </p>
                </div>
                <div className="banner-art" aria-hidden="true">
                  <div className="art-circle" />
                  <BookOpen size={84} strokeWidth={1.2} />
                  <span className="mini-star">✦</span>
                </div>
              </section>
              <div className="stats">
                <div>
                  <span className="stat-icon">
                    <BookOpen size={22} />
                  </span>
                  <div>
                    <small>进行中的班级</small>
                    <strong>
                      {active.length}
                      <em>个</em>
                    </strong>
                  </div>
                </div>
                <div>
                  <span className="stat-icon neutral">
                    <Users size={22} />
                  </span>
                  <div>
                    <small>
                      {user.role === "teacher"
                        ? "班级学生总人次"
                        : "班级同学总人次"}
                    </small>
                    <strong>
                      {totalMembers}
                      <em>人次</em>
                    </strong>
                  </div>
                </div>
                <div>
                  <span className="stat-icon neutral">
                    <Files size={22} />
                  </span>
                  <div>
                    <small>共享教学讲义</small>
                    <strong>
                      {totalFiles}
                      <em>份</em>
                    </strong>
                  </div>
                </div>
              </div>
              <div className="list-toolbar">
                <div className="filter-tabs">
                  {[
                    ["active", "进行中"],
                    ["archived", "已归档"],
                    ["all", "全部班级"],
                  ].map(([key, label]) => (
                    <button
                      key={key}
                      className={filter === key ? "active" : ""}
                      onClick={() => setFilter(key)}
                    >
                      {label}
                      {key === "active" && <span>{active.length}</span>}
                    </button>
                  ))}
                </div>
                <div className="search">
                  <Search size={17} />
                  <input
                    aria-label="搜索班级"
                    placeholder="搜索班级名称"
                    value={search}
                    onChange={(e) => setSearch(e.target.value)}
                  />
                </div>
              </div>
              {loading ? (
                <Busy />
              ) : filtered.length === 0 ? (
                <div className="panel">
                  <Empty
                    title={
                      classes.length === 0
                        ? user.role === "teacher"
                          ? "开启你的第一间课堂"
                          : "你的学习旅程，从加入班级开始"
                        : "这里还没有班级"
                    }
                    description={
                      classes.length === 0
                        ? user.role === "teacher"
                          ? "创建班级，邀请同学，让知识在这里发生。"
                          : "向老师获取邀请码，加入班级查看学习资料。"
                        : "尝试切换分类，或使用其他搜索关键词。"
                    }
                    action={classes.length === 0 ? actionButton() : undefined}
                  />
                </div>
              ) : (
                <div className="class-grid">
                  {filtered.map((c, i) => (
                    <button
                      key={c.id}
                      className="class-card"
                      onClick={() => go("class/" + c.id)}
                    >
                      <div className={"card-cover tone-" + (i % 3)}>
                        <div className="cover-lines" />
                        <BookOpen size={43} strokeWidth={1.2} />
                        <span className="badge">
                          {c.archived ? "已归档" : "进行中"}
                        </span>
                        <span className="cover-caption">
                          KNOWLEDGE CONNECTS US
                        </span>
                      </div>
                      <div className="card-body">
                        <h3>{c.name}</h3>
                        <p>{c.description || "每一次相遇，都是成长的开始。"}</p>
                        <div className="card-teacher">
                          <span className="small-avatar">{c.teacher[0]}</span>
                          {c.teacher}
                          <span>任课教师</span>
                        </div>
                        <div className="card-footer">
                          <span>
                            <Users size={15} />
                            {c.member_count} 位同学
                          </span>
                          <span>
                            <Files size={15} />
                            {c.handout_count} 份讲义
                          </span>
                          <ArrowUpRight size={18} />
                        </div>
                      </div>
                    </button>
                  ))}
                </div>
              )}
            </>
          )}
          {pageError && (
            <div className="error page-error" role="alert">
              {pageError}
              <button className="text-button" onClick={refresh}>
                重新加载
              </button>
            </div>
          )}
          <footer className="page-footer">
            <span>知序 · 让教与学，有序发生</span>
            <span>专注当下的每一份成长</span>
          </footer>
        </main>
      </div>
      {toast && (
        <div className="toast" role="status">
          <Check size={17} />
          {toast}
        </div>
      )}
      {dialog && (
        <Modal title={dialog.title} close={() => setDialog(null)} busy={busy}>
          <form onSubmit={submit}>
            {dialog.kind === "confirm" ? (
              <p className="confirm-copy">{dialog.text}</p>
            ) : (
              <div className="modal-fields">
                {(dialog.kind === "create" || dialog.kind === "edit") && (
                  <>
                    <label>
                      班级名称
                      <input
                        name="name"
                        required
                        maxLength={80}
                        placeholder="例如：互联网应用开发 · 2026 秋"
                        defaultValue={
                          dialog.kind === "edit" ? detail?.classroom.name : ""
                        }
                      />
                    </label>
                    <label>
                      班级简介 <span className="optional">选填</span>
                      <textarea
                        name="description"
                        rows={3}
                        maxLength={1000}
                        placeholder="介绍一下课程内容、学习目标…"
                        defaultValue={
                          dialog.kind === "edit"
                            ? detail?.classroom.description
                            : ""
                        }
                      />
                    </label>
                    <p className="field-tip">
                      创建后会自动生成专属邀请码，分享给学生即可加入。
                    </p>
                  </>
                )}
                {dialog.kind === "join" && (
                  <>
                    <div className="dialog-intro">
                      <KeyRound size={27} />
                      <p>输入老师分享的邀请码，加入你的班级。</p>
                    </div>
                    <label>
                      班级邀请码
                      <input
                        className="code-input"
                        name="code"
                        placeholder="请输入 10 位邀请码"
                        minLength={10}
                        maxLength={10}
                        required
                        autoComplete="off"
                      />
                    </label>
                  </>
                )}
                {dialog.kind === "teacher" && (
                  <>
                    <label>
                      教师姓名
                      <input
                        name="name"
                        maxLength={40}
                        required
                        placeholder="请输入教师姓名"
                      />
                    </label>
                    <label>
                      登录账号
                      <input
                        name="username"
                        autoComplete="off"
                        pattern="[A-Za-z0-9_]{3,32}"
                        title="3–32 位字母、数字或下划线"
                        minLength={3}
                        maxLength={32}
                        required
                        placeholder="字母、数字或下划线"
                      />
                    </label>
                    <label>
                      初始密码
                      <input
                        name="password"
                        type="password"
                        minLength={8}
                        maxLength={72}
                        required
                        autoComplete="new-password"
                        placeholder="至少 8 位密码"
                      />
                    </label>
                    <p className="field-tip">
                      <LockKeyhole size={14} />
                      请将账号和密码通过安全方式告知教师。
                    </p>
                  </>
                )}
                {dialog.kind === "upload" && (
                  <>
                    <label
                      className={
                        "upload-zone " + (selectedFile ? "has-file" : "")
                      }
                      onDragOver={(e) => e.preventDefault()}
                      onDrop={(e) => {
                        e.preventDefault();
                        const f = e.dataTransfer.files[0];
                        if (f) setSelectedFile(f);
                      }}
                    >
                      <UploadCloud size={35} />
                      <strong>
                        {selectedFile
                          ? selectedFile.name
                          : "点击选择或拖拽文件到这里"}
                      </strong>
                      <span>
                        {selectedFile
                          ? size(selectedFile.size)
                          : "支持 PDF、Word、PPT、TXT、MD，单个文件不超过 50 MB"}
                      </span>
                      <input
                        type="file"
                        aria-label="选择讲义文件"
                        accept=".pdf,.doc,.docx,.ppt,.pptx,.txt,.md"
                        onChange={(e) =>
                          setSelectedFile(e.target.files?.[0] || null)
                        }
                      />
                    </label>
                    <label>
                      讲义标题 <span className="optional">选填</span>
                      <input
                        name="title"
                        maxLength={120}
                        placeholder="留空则使用文件名"
                      />
                    </label>
                    <p className="field-tip">
                      TXT/MD 请使用 UTF-8 编码。上传后，班级内同学可下载；PDF
                      支持在线预览。
                    </p>
                  </>
                )}
              </div>
            )}
            {formError && (
              <p className="error" role="alert">
                {formError}
              </p>
            )}
            <div className="modal-actions">
              <button
                type="button"
                className="secondary"
                onClick={() => setDialog(null)}
                disabled={busy}
              >
                取消
              </button>
              <button className="primary" disabled={busy}>
                {busy ? (
                  <>
                    <LoaderCircle size={17} className="spin" />
                    {dialog.kind === "upload" ? "正在上传…" : "正在处理…"}
                  </>
                ) : dialog.kind === "upload" ? (
                  "上传讲义"
                ) : dialog.kind === "join" ? (
                  "加入班级"
                ) : dialog.kind === "teacher" ? (
                  "创建账号"
                ) : dialog.kind === "create" ? (
                  "创建班级"
                ) : dialog.kind === "edit" ? (
                  "保存修改"
                ) : (
                  "确认"
                )}
              </button>
            </div>
          </form>
        </Modal>
      )}
    </div>
  );
}
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
