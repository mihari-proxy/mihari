"""Behavioral contracts for the Mihari GitHub Pages landing page."""

from __future__ import annotations

from html.parser import HTMLParser
import json
from pathlib import Path
import re
import subprocess
import xml.etree.ElementTree as ET


ROOT = Path(__file__).resolve().parents[2]
SITE = ROOT / "site"
SITE_ORIGIN = "https://mihari-proxy.github.io/mihari"
REPOSITORY = "https://github.com/mihari-proxy/mihari"


class Page(HTMLParser):
    """Collect the rendered document inputs that matter to users and crawlers."""

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.attrs: list[tuple[str, dict[str, str]]] = []
        self.text: list[str] = []
        self.title: list[str] = []
        self.json_ld: list[str] = []
        self._in_title = False
        self._in_json_ld = False

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        values = {key: value or "" for key, value in attrs}
        self.attrs.append((tag, values))
        self._in_title = tag == "title"
        self._in_json_ld = tag == "script" and values.get("type") == "application/ld+json"

    def handle_endtag(self, tag: str) -> None:
        if tag == "title":
            self._in_title = False
        if tag == "script":
            self._in_json_ld = False

    def handle_data(self, data: str) -> None:
        stripped = data.strip()
        if stripped:
            self.text.append(stripped)
        if self._in_title:
            self.title.append(data)
        if self._in_json_ld:
            self.json_ld.append(data)


def parse_page(relative: str) -> tuple[Path, Page]:
    path = SITE / relative
    page = Page()
    page.feed(path.read_text(encoding="utf-8"))
    return path, page


def attrs_for(page: Page, tag: str) -> list[dict[str, str]]:
    return [attrs for current, attrs in page.attrs if current == tag]


def test_landing_page_delivers_bilingual_interactive_demo() -> None:
    """Catch a deployment that omits a language entry or the simulated UI."""
    required = (
        "index.html",
        "zh/index.html",
        "styles.css",
        "demo.js",
        "install.js",
        "particles.js",
        "favicon.svg",
        "robots.txt",
        "sitemap.xml",
        ".nojekyll",
    )
    missing = [relative for relative in required if not (SITE / relative).is_file()]
    assert missing == []


def test_english_page_is_an_indexable_mihari_entry() -> None:
    """Catch metadata that cannot identify this project or its language routes."""
    _, page = parse_page("index.html")
    title = "".join(page.title).lower()
    assert all(term in title for term in ("mihari", "mihomo", "cli", "tui"))

    html = attrs_for(page, "html")[0]
    assert html["lang"] == "en"

    links = attrs_for(page, "link")
    assert any(item.get("rel") == "canonical" and item.get("href") == f"{SITE_ORIGIN}/" for item in links)
    alternates = {(item.get("hreflang"), item.get("href")) for item in links if item.get("rel") == "alternate"}
    assert ("en", f"{SITE_ORIGIN}/") in alternates
    assert ("zh-CN", f"{SITE_ORIGIN}/zh/") in alternates

    descriptions = [item.get("content", "").lower() for item in attrs_for(page, "meta") if item.get("name") == "description"]
    assert descriptions and all(term in descriptions[0] for term in ("mihomo", "windows", "linux", "macos"))

    structured = json.loads("".join(page.json_ld))
    assert structured["@type"] == "SoftwareApplication"
    assert structured["url"] == REPOSITORY


def test_primary_actions_and_all_tui_pages_are_present_in_html() -> None:
    """Catch a broken GitHub path or an incomplete no-JavaScript demo fallback."""
    _, page = parse_page("index.html")
    anchors = attrs_for(page, "a")
    assert any(item.get("href") == REPOSITORY for item in anchors)
    assert any(item.get("href") == f"{REPOSITORY}/releases" for item in anchors)

    page_ids = {"overview", "proxies", "connections", "rules", "logs", "subscriptions", "webgui", "system"}
    controls = {
        item.get("data-demo-target")
        for item in attrs_for(page, "button")
        if item.get("data-demo-target")
    }
    panels = {
        item.get("data-demo-page")
        for _, item in page.attrs
        if item.get("data-demo-page")
    }
    assert controls == page_ids
    assert panels == page_ids
    assert attrs_for(page, "img") == []
    assert list(SITE.rglob("*.png")) == []


def test_proxy_and_webgui_simulations_match_the_real_tui_information_model() -> None:
    """Catch a return to generic proxy tables or product-style Web GUI cards."""
    for relative in ("index.html", "zh/index.html"):
        _, page = parse_page(relative)
        classes = [
            token
            for _, attrs in page.attrs
            for token in attrs.get("class", "").split()
        ]
        copy = " ".join(page.text)

        assert classes.count("proxy-group-section") >= 4
        assert classes.count("proxy-node-card") == 5
        assert classes.count("webgui-panel-section") == 2
        assert "proxy-list" not in classes
        assert "panel-logo" not in classes

        assert all(term in copy for term in ("mihari.ghio", "SELECTOR", "Now:", "VLESS / XUDP"))
        assert all(term in copy for term in ("Installed", "Latest", "Health", "Rollback", "Gateway safeguards"))

    css = (SITE / "styles.css").read_text(encoding="utf-8")
    assert re.search(r"\.proxy-node-grid\s*\{[^}]*minmax\(16[0-9]px", css)
    assert re.search(r"\.proxy-node-card > div\s*\{[^}]*white-space:\s*nowrap", css)
    assert re.search(r"\.delay\s*\{[^}]*white-space:\s*nowrap", css)


def _contains_consecutive(page: Page, words: tuple[str, ...]) -> bool:
    for index in range(len(page.text) - len(words) + 1):
        if tuple(page.text[index : index + len(words)]) == words:
            return True
    return False


def test_subscription_simulation_is_a_full_width_status_table() -> None:
    """Catch a return to promotional subscription cards instead of the TUI table."""
    columns = ("Name", "Active", "State", "Load", "Proxy", "Traffic", "Last update", "Next update")
    for relative in ("index.html", "zh/index.html"):
        _, page = parse_page(relative)
        classes = [
            token
            for _, attrs in page.attrs
            for token in attrs.get("class", "").split()
        ]
        copy = " ".join(page.text)

        assert "subscription-card" not in classes
        assert "subscription-list" not in classes
        assert "subscriptions-table" in classes
        assert "Subscriptions · 3" in copy
        assert all(column in copy for column in columns)
        assert all(term in copy for term in ("mihari.ghio", "Enabled", "Live", "AUTO", "Cached", "DIRECT"))
        assert "a add" in copy and "Ctrl+R refresh all" in copy


def test_demo_system_ports_match_mihari_defaults() -> None:
    """Catch Clash mixed-port 7890 or swapped Mixed/Controller/Web defaults."""
    for relative in ("index.html", "zh/index.html"):
        html = (SITE / relative).read_text(encoding="utf-8")
        _, page = parse_page(relative)
        assert "7890" not in html
        assert _contains_consecutive(page, ("Mixed", "9190 · Owned"))
        assert _contains_consecutive(page, ("Controller", "9090 · Owned"))
        assert _contains_consecutive(page, ("Web", "9191 · Owned"))
        assert "127.0.0.1:9191" in " ".join(page.text)


def test_site_does_not_use_kanata_branding() -> None:
    """Catch any remaining third-party Kanata branding on the public site."""
    leaked = []
    for path in SITE.rglob("*"):
        if not path.is_file() or path.suffix.lower() not in {".html", ".css", ".js", ".svg", ".xml", ".txt"}:
            continue
        text = path.read_text(encoding="utf-8")
        if re.search(r"kanata", text, re.I):
            leaked.append(str(path.relative_to(SITE)))
    assert leaked == []
    _, page = parse_page("index.html")
    assert "mihari.ghio" in " ".join(page.text)


def test_memory_comparison_is_an_independent_homepage_section() -> None:
    """Catch the TUI memory story collapsing into the demo carousel or vanishing."""
    for relative in ("index.html", "zh/index.html"):
        _, page = parse_page(relative)
        ids = [attrs.get("id") for _, attrs in page.attrs]
        classes = {
            token
            for _, attrs in page.attrs
            for token in attrs.get("class", "").split()
        }
        copy = " ".join(page.text)

        assert "memory" in ids
        assert ids.index("memory") > ids.index("architecture-title")
        assert ids.index("install") > ids.index("memory")
        assert "memory-comparison" in classes
        assert "memory-meter" in classes
        assert "memory-saving" in classes
        assert "memory-process" in classes
        card_order = [
            token
            for _, attrs in page.attrs
            for token in attrs.get("class", "").split()
            if token in {"memory-card-electron", "memory-card-tui"}
        ]
        assert card_order == ["memory-card-electron", "memory-card-tui"]
        assert "258.5 MB" in copy
        assert "106.6 MB" in copy
        assert "44.1 MB" in copy
        assert "101.2 MB" in copy
        assert "6.5 MB" in copy
        assert "10.7 MB" in copy
        assert "92.5 MB" in copy
        assert "103.2 MB" in copy
        assert "mihari.exe" in copy
        assert "mihomo.exe" in copy
        assert "-60%" in copy
        assert "MEM Usage" in copy
        assert "Memory Usage" not in copy
        assert _contains_consecutive(page, ("103.2 MB", "-60% MEM Usage"))
        html = (SITE / relative).read_text(encoding="utf-8")
        tui_at = html.find("memory-card-tui")
        saving_at = html.find("memory-saving")
        assert 0 < tui_at < saving_at
        assert "254.9 MB" not in copy
        assert "23.6 MB" not in copy
        assert "-59%" not in copy
        assert "Sparkle" in copy
        assert "observed" in copy.lower() or "观测" in copy

        assert "memory" not in {attrs.get("data-demo-page") for _, attrs in page.attrs}


def test_memory_cards_stretch_to_the_same_row_height() -> None:
    """Catch sibling memory cards shrinking to their own content height."""
    css = (SITE / "styles.css").read_text(encoding="utf-8")
    card = re.search(r"\.memory-card\s*\{([^}]+)\}", css)
    process = re.search(r"\.memory-process\s*\{([^}]+)\}", css)
    comparison = re.search(r"\.memory-comparison\s*\{([^}]+)\}", css)
    assert card and process and comparison
    assert "align-items: stretch" in comparison.group(1)
    assert "height: 100%" not in card.group(1)
    assert "flex-direction: column" in card.group(1)
    assert "flex: 1" in process.group(1)
    assert re.search(r"\.memory-process li\s*\{[^}]*display:\s*contents", css)
    assert "tabular-nums" in process.group(1) or "tabular-nums" in css


def test_install_heading_is_three_left_aligned_lines() -> None:
    """Catch the install title collapsing back into a single marketing sentence."""
    _, english = parse_page("index.html")
    heading = [
        attrs
        for tag, attrs in english.attrs
        if tag == "h2" and attrs.get("id") == "install-title"
    ]
    assert heading and "install-title" in heading[0].get("class", "").split()
    assert _contains_consecutive(english, ("Get", "Installation", "Command"))
    assert "Choose a path. Copy one command." not in " ".join(english.text)

    _, chinese = parse_page("zh/index.html")
    chinese_heading = [
        attrs
        for tag, attrs in chinese.attrs
        if tag == "h2" and attrs.get("id") == "install-title"
    ]
    assert chinese_heading and "install-title" in chinese_heading[0].get("class", "").split()
    assert "获取安装命令" in chinese.text
    assert not _contains_consecutive(chinese, ("获取", "安装", "命令"))
    assert "选择安装路径，复制一条命令。" not in " ".join(chinese.text)
    assert "<span>获取</span>" not in (SITE / "zh/index.html").read_text(encoding="utf-8")


def test_architecture_visualizes_control_and_commit_paths() -> None:
    """Catch architecture copy collapsing back into three disconnected banners."""
    for relative in ("index.html", "zh/index.html"):
        _, page = parse_page(relative)
        classes = {
            token
            for _, attrs in page.attrs
            for token in attrs.get("class", "").split()
        }
        copy = " ".join(page.text).lower()

        assert {
            "architecture-blueprint",
            "architecture-surfaces",
            "architecture-daemon",
            "architecture-runtime",
            "architecture-commit-flow",
        } <= classes
        assert "architecture-row" not in classes
        assert all(
            term in copy
            for term in (
                "native ipc",
                "web gateway",
                "mihari daemon",
                "validate",
                "atomic replace",
                "reload",
                "rollback",
            )
        )


def test_copy_matches_the_daemon_owned_local_architecture() -> None:
    """Catch generic proxy-site copy that misrepresents Mihari's boundaries."""
    _, english = parse_page("index.html")
    copy = " ".join(english.text).lower()
    required = (
        "one daemon",
        "cli",
        "tui",
        "web",
        "named pipe",
        "unix domain socket",
        "cgo-free",
        "atomic",
        "rollback",
        "windows",
        "linux",
        "macos",
    )
    assert [term for term in required if term not in copy] == []
    assert "vpn" not in copy

    _, chinese = parse_page("zh/index.html")
    chinese_copy = " ".join(chinese.text).lower()
    assert all(term in chinese_copy for term in ("单一守护进程", "本地命名管道", "unix 域套接字", "原子", "回滚"))


def test_every_local_reference_survives_the_github_pages_subpath() -> None:
    """Catch root-relative or missing assets that break under /mihari/."""
    for relative in ("index.html", "zh/index.html"):
        path, page = parse_page(relative)
        for _, attrs in page.attrs:
            for attribute in ("href", "src"):
                reference = attrs.get(attribute, "")
                if not reference or reference.startswith(("https://", "http://", "mailto:", "#", "data:")):
                    continue
                assert not reference.startswith("/"), reference
                asset = (path.parent / re.split(r"[?#]", reference, maxsplit=1)[0]).resolve()
                asset.relative_to(SITE.resolve())
                assert asset.exists(), f"{relative}: {reference}"


def test_motion_is_accessible_and_background_comes_from_tsparticles() -> None:
    """Catch hand-built background effects or motion that ignores preferences."""
    css = (SITE / "styles.css").read_text(encoding="utf-8").lower()
    assert "prefers-reduced-motion: reduce" in css
    assert "animation-duration: 0.01ms" in css
    assert ":focus-visible" in css
    assert "ambient-beam" not in css
    assert "ambient-grid" not in css

    _, page = parse_page("index.html")
    particle_hosts = [attrs for _, attrs in page.attrs if attrs.get("id") == "tsparticles"]
    assert particle_hosts and all(item.get("aria-hidden") == "true" for item in particle_hosts)
    assert not any(item.get("data-ambient") == "" for _, item in page.attrs)

    scripts = [item.get("src") for item in attrs_for(page, "script") if item.get("src")]
    assert "https://cdn.jsdelivr.net/npm/@tsparticles/engine@4/tsparticles.engine.min.js" in scripts
    assert "https://cdn.jsdelivr.net/npm/@tsparticles/slim@4/tsparticles.slim.bundle.min.js" in scripts
    assert "particles.js" in scripts
    assert "demo.js" in scripts


def _declared_font_px(css: str) -> list[float]:
    sizes: list[float] = []
    sizes.extend(float(value) for value in re.findall(r"font-size\s*:\s*(\d+(?:\.\d+)?)px", css, re.I))
    for shorthand in re.finditer(r"(?<!-)font\s*:\s*([^;]+)", css, re.I):
        sizes.extend(float(value) for value in re.findall(r"(\d+(?:\.\d+)?)px", shorthand.group(1)))
    return sizes


def _clamp_max_rems(css: str, selector: str) -> list[float]:
    maxima: list[float] = []
    for arguments in re.findall(
        rf"{re.escape(selector)}\s*\{{[^}}]*font-size\s*:\s*clamp\(([^)]+)\)",
        css,
    ):
        maximum = arguments.split(",")[-1].strip()
        match = re.fullmatch(r"(\d+(?:\.\d+)?)rem", maximum)
        assert match, maximum
        maxima.append(float(match.group(1)))
    assert maxima, selector
    return maxima


def test_page_uses_deep_gray_background_and_readable_type() -> None:
    """Catch a return to near-black canvas or 7–8px microcopy."""
    css = (SITE / "styles.css").read_text(encoding="utf-8")
    assert re.search(r"--bg:\s*#111113\b", css)
    assert not re.search(r"--bg:\s*#09090b\b", css)
    assert ".install-title" in css
    assert re.search(r"\.install-title[^{]*\{[^}]*text-align:\s*left", css, re.S)

    undersized = [size for size in _declared_font_px(css) if size < 12]
    assert undersized == []
    assert max(_clamp_max_rems(css, ".hero h1")) <= 3.2
    assert max(_clamp_max_rems(css, ".section-heading h2")) <= 2.6
    assert max(_clamp_max_rems(css, ".final-cta h2")) <= 3.4

    for relative in ("index.html", "zh/index.html"):
        _, page = parse_page(relative)
        themes = [item.get("content") for item in attrs_for(page, "meta") if item.get("name") == "theme-color"]
        assert themes == ["#111113"]


def test_chinese_display_type_uses_cjk_metrics() -> None:
    """Catch Chinese display headings inheriting Latin tracking and line-height."""
    css = (SITE / "styles.css").read_text(encoding="utf-8")
    assert re.search(r'html\[lang="zh-CN"\] \.hero h1', css)
    assert re.search(r'html\[lang="zh-CN"\] \.hero h1[^}]*letter-spacing:\s*0', css, re.S)
    assert re.search(r'html\[lang="zh-CN"\] \.hero h1[^}]*line-height:\s*1\.(1[5-9]|2\d?)', css, re.S)
    zh_hero = max(_clamp_max_rems(css, 'html[lang="zh-CN"] .hero h1'))
    en_hero = max(_clamp_max_rems(css, ".hero h1"))
    assert zh_hero < en_hero
    assert zh_hero <= 2.7

    _, english = parse_page("index.html")
    _, chinese = parse_page("zh/index.html")
    assert attrs_for(english, "html")[0]["lang"] == "en"
    assert attrs_for(chinese, "html")[0]["lang"] == "zh-CN"


def test_demo_autoplay_and_manual_pause_timing() -> None:
    """Catch regressions in the 3.6-second cycle and 10-second manual pause."""
    result = subprocess.run(
        ["node", str(Path(__file__).with_name("test_demo_state.cjs"))],
        cwd=ROOT,
        check=False,
        capture_output=True,
        text=True,
    )
    assert result.returncode == 0, result.stdout + result.stderr


def test_install_builder_has_only_supported_choices() -> None:
    """Catch missing install paths or a version selector the product does not offer."""
    _, page = parse_page("index.html")
    anchors = attrs_for(page, "a")
    assert any(item.get("href") == "#install" for item in anchors)

    install_sections = [item for _, item in page.attrs if item.get("id") == "install"]
    assert len(install_sections) == 1

    source_choices = {
        item.get("data-install-source")
        for item in attrs_for(page, "button")
        if item.get("data-install-source")
    }
    os_choices = {
        item.get("data-install-os")
        for item in attrs_for(page, "button")
        if item.get("data-install-os")
    }
    channel_choices = {
        item.get("data-install-channel")
        for item in attrs_for(page, "button")
        if item.get("data-install-channel")
    }
    assert source_choices == {"github", "offline"}
    assert os_choices == {"linux", "macos", "windows"}
    assert channel_choices == {"main", "dev"}
    assert not any("version" in key for _, item in page.attrs for key in item)


def test_install_command_matrix_matches_repository_scripts() -> None:
    """Catch generated commands that point at the wrong branch or installer."""
    result = subprocess.run(
        ["node", str(Path(__file__).with_name("test_install_commands.cjs"))],
        cwd=ROOT,
        check=False,
        capture_output=True,
        text=True,
    )
    assert result.returncode == 0, result.stdout + result.stderr


def test_crawler_files_point_to_both_language_pages() -> None:
    """Catch discovery files that publish only one language route."""
    root = ET.parse(SITE / "sitemap.xml").getroot()
    namespace = {"sm": "http://www.sitemaps.org/schemas/sitemap/0.9"}
    locations = {node.text for node in root.findall(".//sm:loc", namespace)}
    assert locations == {f"{SITE_ORIGIN}/", f"{SITE_ORIGIN}/zh/"}
    robots = (SITE / "robots.txt").read_text(encoding="utf-8")
    assert f"Sitemap: {SITE_ORIGIN}/sitemap.xml" in robots


def test_public_site_does_not_expose_private_control_details() -> None:
    """Catch accidental publication of daemon credentials or controller routes."""
    public_text = "\n".join(
        path.read_text(encoding="utf-8")
        for path in SITE.rglob("*")
        if path.is_file() and path.suffix.lower() in {".html", ".css", ".svg", ".xml", ".txt"}
    ).lower()
    assert "control.token" not in public_text
    assert "external-controller" not in public_text
    assert not re.search(r"subscribe[^\s\"']*token=", public_text)


README_EN = ROOT / "README.md"
README_ZH = ROOT / "README.zh-CN.md"


def _first_heading(text: str) -> re.Match[str] | None:
    return re.search(r"^#{1,6} (.+)$", text, re.MULTILINE)


def _lead(text: str, limit: int = 1800) -> str:
    return text[:limit].lower()


def test_english_readme_h1_names_mihomo_cli_tui() -> None:
    """Catch a README title that still reads as a generic Mihari project."""
    text = README_EN.read_text(encoding="utf-8")
    heading = _first_heading(text)
    assert heading is not None
    assert heading.group(0).startswith("# ")
    assert not heading.group(0).startswith("## ")
    title = heading.group(1).lower()
    assert all(term in title for term in ("mihari", "mihomo", "cli", "tui"))


def test_english_readme_lead_covers_search_terms() -> None:
    """Catch an opening that omits the words people actually search for."""
    lead = _lead(README_EN.read_text(encoding="utf-8"))
    missing = [
        word
        for word in (
            "mihomo",
            "cli",
            "tui",
            "windows",
            "linux",
            "macos",
            "subscription",
            "clash",
        )
        if word not in lead
    ]
    assert missing == []
    assert "non-profit" in lead
    assert "informal development" in lead
    assert f"{SITE_ORIGIN}/" in README_EN.read_text(encoding="utf-8")


def test_chinese_readme_h1_and_lead_cover_search_terms() -> None:
    """Catch the Chinese README drifting out of sync with the English opener."""
    text = README_ZH.read_text(encoding="utf-8")
    heading = _first_heading(text)
    assert heading is not None
    assert heading.group(0).startswith("# ")
    title = heading.group(1).lower()
    assert "mihari" in title
    assert "mihomo" in title
    lead = _lead(text)
    missing = [
        word
        for word in (
            "mihomo",
            "cli",
            "tui",
            "终端",
            "订阅",
            "系统代理",
            "tun",
            "windows",
            "linux",
            "macos",
        )
        if word not in lead
    ]
    assert missing == []
    assert f"{SITE_ORIGIN}/zh/" in text
