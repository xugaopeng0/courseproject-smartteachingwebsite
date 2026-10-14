"""让单个用例脚本可以独立运行：`python apitest/cases/api/auth/login/POST.py`。

用例文件末尾统一加上：

    if __name__ == "__main__":  # 允许直接运行本脚本
        from lib.single import main
        main(__file__)

依赖与数据库配置的解析方式与 run_all.py 完全一致，因此单独运行与整体运行结果一致。
"""

from __future__ import annotations

import sys
from pathlib import Path


def main(case_file: str) -> int:
    apitest_root = Path(case_file).resolve().parents[0]
    while apitest_root.name != "apitest" and apitest_root.parent != apitest_root:
        apitest_root = apitest_root.parent
    if str(apitest_root) not in sys.path:
        sys.path.insert(0, str(apitest_root))

    import run_all  # noqa: PLC0415 - 需要在补好 sys.path 之后再导入
    from lib.runner import Context, discover, run_all as execute  # noqa: PLC0415

    arguments = [str(apitest_root / "run_all.py"), "--filter", _filter_for(case_file)]
    previous = sys.argv
    sys.argv = arguments
    try:
        return run_all.main()
    finally:
        sys.argv = previous


def _filter_for(case_file: str) -> str:
    """用用例文件的相对路径作为过滤器，只跑这一个脚本。"""
    path = Path(case_file).resolve()
    parts = path.parts
    index = parts.index("cases")
    return "/".join(parts[index:])
