"""自包含 HTML 报告：内联样式、无外部资源、不包含密码与令牌。"""

from __future__ import annotations

import html
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Any


@dataclass
class Assertion:
    name: str
    ok: bool
    detail: str = ""


@dataclass
class CaseResult:
    path: str
    method: str
    url: str
    status: int | None
    duration_ms: int
    assertions: list[Assertion] = field(default_factory=list)
    error: str = ""

    @property
    def ok(self) -> bool:
        return not self.error and all(item.ok for item in self.assertions) and bool(self.assertions)


def render(results: list[CaseResult], environment: dict[str, Any], target: Path) -> Path:
    passed = sum(1 for item in results if item.ok)
    total = len(results)
    failed = total - passed
    rows = "\n".join(_case_html(item) for item in results)
    environment_html = "\n".join(
        f"<tr><th>{html.escape(str(key))}</th><td>{html.escape(str(value))}</td></tr>" for key, value in environment.items()
    )
    document = f"""<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8" />
<title>接口测试报告</title>
<style>
  body {{ margin: 0; padding: 28px 32px; font: 14px/1.7 "Microsoft YaHei", system-ui, sans-serif; color: #262730; background: #f8f9fb; }}
  h1 {{ font-size: 20px; margin: 0 0 4px; }}
  .meta {{ color: #858791; font-size: 12px; margin-bottom: 18px; }}
  .summary {{ display: flex; gap: 14px; margin-bottom: 18px; }}
  .card {{ background: #fff; border: 1px solid #edeef1; border-radius: 10px; padding: 12px 18px; min-width: 110px; }}
  .card strong {{ display: block; font-size: 22px; }}
  .card span {{ color: #858791; font-size: 12px; }}
  .ok strong {{ color: #2f7d4f; }}
  .bad strong {{ color: #b82e3c; }}
  table {{ width: 100%; border-collapse: collapse; background: #fff; border: 1px solid #edeef1; border-radius: 10px; overflow: hidden; }}
  th, td {{ text-align: left; padding: 9px 12px; border-bottom: 1px solid #f2f3f5; vertical-align: top; font-size: 13px; }}
  th {{ background: #fafafb; font-weight: 600; color: #62636d; width: 220px; }}
  .case {{ margin-bottom: 16px; border: 1px solid #edeef1; border-radius: 10px; background: #fff; overflow: hidden; }}
  .case-head {{ display: flex; align-items: center; gap: 10px; padding: 11px 14px; border-bottom: 1px solid #f2f3f5; }}
  .tag {{ font-size: 11px; padding: 2px 8px; border-radius: 999px; background: #f2f3f5; color: #62636d; }}
  .tag.ok {{ background: #eaf5ee; color: #2f7d4f; }}
  .tag.bad {{ background: #fbecee; color: #b82e3c; }}
  .case-body {{ padding: 10px 14px; }}
  .case-body code {{ background: #fafafb; padding: 1px 5px; border-radius: 4px; }}
  ul {{ margin: 6px 0 0; padding-left: 18px; }}
  li.bad {{ color: #b82e3c; }}
  li.ok {{ color: #3c3d45; }}
  .detail {{ color: #858791; font-size: 12px; }}
</style>
</head>
<body>
<h1>接口测试报告</h1>
<p class="meta">生成时间 {datetime.now().strftime('%Y-%m-%d %H:%M:%S')} · 运行入口 <code>apitest/run_all.py</code></p>
<div class="summary">
  <div class="card"><strong>{total}</strong><span>用例</span></div>
  <div class="card ok"><strong>{passed}</strong><span>通过</span></div>
  <div class="card {'bad' if failed else ''}"><strong>{failed}</strong><span>失败</span></div>
</div>
<table>{environment_html}</table>
<h2 style="font-size:15px;margin:22px 0 10px">用例明细</h2>
{rows}
</body>
</html>
"""
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(document, encoding="utf-8")
    return target


def _case_html(result: CaseResult) -> str:
    state = "ok" if result.ok else "bad"
    label = "通过" if result.ok else "失败"
    items = "\n".join(
        f'<li class="{"ok" if item.ok else "bad"}">{html.escape(item.name)}'
        + (f'<span class="detail"> — {html.escape(item.detail)}</span>' if item.detail else "")
        + "</li>"
        for item in result.assertions
    )
    error = f'<p class="detail">异常：{html.escape(result.error)}</p>' if result.error else ""
    return f"""<div class="case">
  <div class="case-head">
    <span class="tag {state}">{label}</span>
    <strong>{html.escape(result.method)} {html.escape(result.url)}</strong>
    <span class="detail">{result.status if result.status is not None else "-"} · {result.duration_ms} ms</span>
  </div>
  <div class="case-body">
    <div class="detail">用例文件 <code>{html.escape(result.path)}</code></div>
    {error}
    <ul>{items}</ul>
  </div>
</div>"""
