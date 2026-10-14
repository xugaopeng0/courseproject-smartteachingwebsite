"""POST /api/classes/{id}/knowledge/search — 班级内三模式检索并核对切片来源。

路径参数与顺序在 case.json 中声明，运行器负责解析。

注意：向量通道用「讲义原句」提问来断言。仓库自带的开发替身是字符二元组特征哈希，
不是语义模型，因此改写后的问法不保证达到余弦阈值——这不属于接口缺陷。
"""

QUESTION = "考核方式由什么组成"
VERBATIM = "考核方式由平时作业百分之四十和期末项目百分之六十组成"
UNRELATED = "南极帝企鹅的迁徙路线"


def setup(ctx):
    """路径参数 {id} 需要班级，缺什么就在运行时准备什么。"""
    ctx.ensure_classroom()


def run(ctx):
    context = ctx.ensure_classroom()
    class_id = context["classroom"]["id"]
    token = context["teacher_token"]
    path = f"/api/classes/{class_id}/knowledge/search"

    status, body = ctx.api.request("POST", path, body={"query": QUESTION, "mode": "hybrid", "top_k": 5}, token=token, expect=200)
    ctx.equal("混合检索返回 200", status, 200)
    ctx.equal("状态为 ok", body.get("status"), "ok")
    hits = body.get("hits", [])
    ctx.check("命中至少一条", len(hits) >= 1, f"hits={len(hits)}")
    if hits:
        first = hits[0]
        ctx.check("命中带序号与字符区间", first.get("ordinal", 0) >= 1 and first.get("char_end", 0) > first.get("char_start", 0), str(first.get("ordinal")))
        ctx.check("命中来自本班讲义", first.get("handout_id") == context["handout"]["id"], first.get("handout_id", ""))
        ctx.check("命中正文包含关键内容", "考核方式" in first.get("content", ""), first.get("content", "")[:30])
        chunk_id = first.get("chunk_id")
        chunk_row = ctx.db.query(
            "SELECT c.handout_id, d.class_id FROM knowledge_chunks c JOIN knowledge_documents d ON d.handout_id=c.handout_id WHERE c.id=%s",
            (chunk_id,),
        )
        ctx.check("切片在数据库中存在且属于该班", bool(chunk_row) and chunk_row[0]["class_id"] == class_id, str(chunk_row[:1]))
        source_status, source = ctx.api.request("GET", f"/api/classes/{class_id}/knowledge/chunks/{chunk_id}/source", token=token, expect=200)
        ctx.equal("来源接口可读到同一切片", source.get("content"), first.get("content"))

    for mode in ("keyword", "vector"):
        probe = QUESTION if mode == "keyword" else VERBATIM
        mode_status, mode_body = ctx.api.request("POST", path, body={"query": probe, "mode": mode, "top_k": 5}, token=token, expect=200)
        ctx.equal(f"{mode} 模式回显正确", mode_body.get("mode"), mode)
        hits = mode_body.get("hits", [])
        ctx.check(f"{mode} 模式有命中", len(hits) >= 1, f"hits={len(hits)}")
        if mode == "vector" and hits:
            similarity = hits[0].get("similarity")
            ctx.check("向量模式给出相似度", similarity is not None and similarity >= 0.35, f"similarity={similarity}")
            ctx.check("向量命中的是同一份讲义", hits[0].get("handout_id") == context["handout"]["id"])

    none_status, none_body = ctx.api.request("POST", path, body={"query": UNRELATED, "mode": "hybrid"}, token=token, expect=200)
    ctx.equal("无关问题返回 no_matches", none_body.get("status"), "no_matches")
    ctx.equal("无关问题无命中", len(none_body.get("hits", [])), 0)
    ctx.check("无关问题给出固定文案", none_body.get("message") == "资料中未找到相关内容", str(none_body.get("message")))

    bad_status, _ = ctx.api.request("POST", path, body={"query": QUESTION, "mode": "unknown"}, token=token, expect=400)
    ctx.equal("未知模式返回 400", bad_status, 400)
    return status

if __name__ == "__main__":  # 允许直接运行本脚本：python apitest/cases/api/classes/{id}/knowledge/search/POST.py
    import pathlib
    import sys

    sys.path.insert(0, str(next(p for p in pathlib.Path(__file__).resolve().parents if (p / "run_all.py").exists())))
    from lib.single import main

    main(__file__)
