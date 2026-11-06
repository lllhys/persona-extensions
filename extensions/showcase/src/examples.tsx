import { Button, Chart, Divider, Empty, Field, Link, Loading, Markdown, Metric, Progress, Row, Section, StatGrid, Table, Text } from "@persona/extension/ui";
import type { CallbackAction, ActionOptions } from "@persona/extension/ui";
import type { Translate } from "./locale.ts";
import { defaultMedia, type DemoMedia } from "./media.ts";

export type DemoAction = (id: string, options?: ActionOptions) => CallbackAction;
export function catalog(t: Translate) { return [
  { id: "text", title: t("s022"), subtitle: t("s138"), icon: "doc" },
  { id: "metric", title: t("s070"), subtitle: t("s139"), icon: "gauge" },
  { id: "progress", title: t("s140"), subtitle: t("s141"), icon: "gauge" },
  { id: "row", title: t("s142"), subtitle: t("s143"), icon: "list" },
  { id: "button", title: t("s144"), subtitle: t("s145"), icon: "check" },
  { id: "link", title: t("s146"), subtitle: t("s147"), icon: "link" },
  { id: "field", title: t("s080"), subtitle: t("s148"), icon: "settings" },
  { id: "chart", title: t("s149"), subtitle: t("s150"), icon: "chart" },
  { id: "stat_grid", title: t("s151"), subtitle: t("s152"), icon: "gauge" },
  { id: "segmented", title: t("s153"), subtitle: t("s154"), icon: "tag" },
  { id: "markdown", title: t("s155"), subtitle: t("s156"), icon: "doc" },
  { id: "table", title: t("s157"), subtitle: t("s158"), icon: "list" },
  { id: "empty", title: t("s159"), subtitle: t("s160"), icon: "folder" },
  { id: "loading", title: t("s161"), subtitle: t("s162"), icon: "clock" },
  { id: "divider", title: t("s163"), subtitle: t("s164"), icon: "list" },
  { id: "list", title: t("s165"), subtitle: t("s166"), icon: "search" },
  { id: "invalidate", title: t("s167"), subtitle: t("s168"), icon: "bell" }
]; }
export function catalogIntroduction(t: Translate) { return t("s169"); }
export function Examples({ page, t, act, media = defaultMedia }: { page: string; t: Translate; act: DemoAction; media?: Readonly<DemoMedia> }) {
  switch (page) {
// Fixed SDK JSX vocabulary; no view-to-element adapter runs in clients.
    case "text": return <>
      <Section key="text-section-0" title={t("s000")}>
        <Text key="text-text-item-0-0" text={t("s001")} />
        <Text key="text-text-item-0-1" text={t("s002")} style={"secondary"} />
        <Text key="text-text-item-0-2" text={t("s003")} style={"caption"} />
        <Text key="text-text-item-0-3" text={t("s004")} style={"code"} />
      </Section>
      <Section key="text-section-1" title={t("s005")} footer={t("s006")}>
        <Text key="text-text-item-1-0" text={t("s007")} />
      </Section>
    </>;
    case "metric": return <>
      <Section key="metric-section-0" >
        <Metric key="metric-metric-item-0-0" label={t("s008")} value={"1,234"} />
        <Metric key="metric-metric-item-0-1" label={t("s009")} value={"¥36.20"} detail={t("s010")} tone={"neutral"} />
        <Metric key="metric-metric-item-0-2" label={t("s011")} value={"¥36.20"} detail={t("s010")} tone={"positive"} />
        <Metric key="metric-metric-item-0-3" label={t("s012")} value={"¥36.20"} detail={t("s010")} tone={"caution"} />
        <Metric key="metric-metric-item-0-4" label={t("s013")} value={"¥36.20"} detail={t("s010")} tone={"danger"} />
        <Metric key="metric-metric-item-0-5" label={t("s014")} value={"999999999999999999999999"} detail={t("s015")} />
      </Section>
    </>;
    case "progress": return <>
      <Section key="progress-section-0" >
        <Progress key="progress-progress-item-0-0" label={t("s016")} detail={t("s017")} ratio={0} />
        <Progress key="progress-progress-item-0-1" label={t("s018")} detail={t("s019")} ratio={0.3} />
        <Progress key="progress-progress-item-0-2" label={t("s020")} detail={t("s021")} ratio={1} />
        <Progress key="progress-progress-item-0-3" label={t("s009")} tone={"neutral"} ratio={0.75} />
        <Progress key="progress-progress-item-0-4" label={t("s011")} tone={"positive"} ratio={0.75} />
        <Progress key="progress-progress-item-0-5" label={t("s012")} tone={"caution"} ratio={0.75} />
        <Progress key="progress-progress-item-0-6" label={t("s013")} tone={"danger"} ratio={0.75} />
      </Section>
    </>;
    case "row": return <>
      <Section key="row-section-0" title={t("s022")}>
        <Row key="row-plain" title={t("s023")} />
        <Row key="row-sub" title={t("s024")} subtitle={t("s025")} />
        <Row key="row-value" value={t("s026")} title={t("s027")} />
        <Row key="row-long" value={t("s028")} title={t("s029")} subtitle={t("s030")} />
        <Row key="row-tap" title={t("s031")} subtitle={t("s032")} action={act("toast")} />
      </Section>
      <Section key="row-section-1" title={t("s033")}>
        <Row key="row-badge-neutral" title={t("s034")} badge={{ "text": t("s035"), "tone": "neutral" }} />
        <Row key="row-badge-positive" title={t("s036")} badge={{ "text": t("s037"), "tone": "positive" }} />
        <Row key="row-badge-caution" title={t("s038")} badge={{ "text": t("s039"), "tone": "caution" }} />
        <Row key="row-badge-danger" title={t("s040")} badge={{ "text": t("s041"), "tone": "danger" }} />
      </Section>
      <Section key="row-section-2" title={t("s042")}>
        <Row key="row-icon" title={t("s043")} icon={"star"} />
        <Row key="row-avatar-text" title={t("s044")} subtitle={t("s045")} avatar={{ "text": t("s046") }} />
        <Row key="row-avatar-url" title={t("s047")} subtitle={t("s048")} avatar={{ "url": media.avatar, "text": t("s049") }} />
        <Row key="row-thumb-square" title={t("s050")} subtitle={t("s051")} thumbnail={{ "url": media.square, "aspect": "square" }} />
        <Row key="row-thumb-wide" title={t("s052")} subtitle={t("s053")} thumbnail={{ "url": media.wide, "aspect": "wide" }} />
        <Row key="row-thumb-broken" title={t("s054")} subtitle={t("s055")} thumbnail={{ "url": media.broken }} />
      </Section>
    </>;
    case "button": return <>
      <Section key="button-section-0" >
        <Button key="button-button-item-0-0" title={t("s056")} action={act("toast")} />
        <Button key="button-button-item-0-1" title={t("s057")} action={act("delete", { "confirm": t("s058") })} role={"destructive"} />
        <Button key="button-button-item-0-2" title={t("s059")} action={act("open-url")} />
        <Button key="button-button-item-0-3" title={t("s060")} action={act("toast")} />
      </Section>
    </>;
    case "link": return <>
      <Section key="link-section-0" >
        <Link key="link-link-item-0-0" title={t("s061")} url={"https://example.com/"} />
        <Link key="link-link-item-0-1" title={t("s062")} url={"https://example.com/a/very/long/path?with=query"} />
      </Section>
    </>;
    case "field": return <>
      <Section key="field-section-0" title={t("s063")}>
        <Field key="field-field-item-0-0" label={t("s022")} value={t("s064")} name={"text"} kind={"text"} placeholder={t("s065")} />
        <Field key="field-field-item-0-1" label={t("s066")} name={"empty"} kind={"text"} placeholder={t("s067")} />
        <Field key="field-field-item-0-2" label={t("s068")} name={"secret"} kind={"secret"} placeholder={t("s069")} />
        <Field key="field-field-item-0-3" label={t("s070")} value={"42"} name={"number"} kind={"number"} />
        <Field key="field-field-item-0-4" label={t("s071")} value={"true"} name={"toggle"} kind={"toggle"} />
        <Field key="field-field-item-0-5" label={t("s072")} value={"b"} name={"choice"} kind={"choice"} choices={[{ "value": "a", "title": t("s073") }, { "value": "b", "title": t("s074") }, { "value": "c", "title": t("s075") }]} />
      </Section>
      <Section key="field-section-1" >
        <Button key="field-field-item-1-0" title={t("s076")} action={act("submit", { "submit": true })} />
      </Section>
    </>;
    case "chart": return <>
      <Section key="chart-section-0" title={t("s077")}>
        <Chart key="chart-chart-item-0-0" detail={t("s078")} title={t("s079")} kind={"bar"} series={[{ "name": t("s080"), "points": [{ "label": t("s081"), "value": 1200000 }, { "label": t("s082"), "value": 800000 }, { "label": t("s083"), "value": 1500000 }, { "label": t("s084"), "value": 900000 }, { "label": t("s085"), "value": 2100000 }, { "label": t("s086"), "value": 1700000 }, { "label": t("s087"), "value": 600000 }] }, { "name": t("s088"), "points": [{ "label": t("s081"), "value": 300000 }, { "label": t("s082"), "value": 250000 }, { "label": t("s083"), "value": 410000 }, { "label": t("s084"), "value": 200000 }, { "label": t("s085"), "value": 520000 }, { "label": t("s086"), "value": 380000 }, { "label": t("s087"), "value": 150000 }] }]} unit={"tokens"} />
      </Section>
      <Section key="chart-section-1" title={t("s089")}>
        <Chart key="chart-chart-item-1-0" title={t("s079")} kind={"bar"} series={[{ "name": t("s080"), "points": [{ "label": t("s081"), "value": 1200000 }, { "label": t("s082"), "value": 800000 }, { "label": t("s083"), "value": 1500000 }, { "label": t("s084"), "value": 900000 }, { "label": t("s085"), "value": 2100000 }, { "label": t("s086"), "value": 1700000 }, { "label": t("s087"), "value": 600000 }] }, { "name": t("s088"), "points": [{ "label": t("s081"), "value": 300000 }, { "label": t("s082"), "value": 250000 }, { "label": t("s083"), "value": 410000 }, { "label": t("s084"), "value": 200000 }, { "label": t("s085"), "value": 520000 }, { "label": t("s086"), "value": 380000 }, { "label": t("s087"), "value": 150000 }] }]} unit={"tokens"} stacked={true} />
      </Section>
      <Section key="chart-section-2" title={t("s090")}>
        <Chart key="chart-chart-item-2-0" detail={t("s091")} title={t("s092")} kind={"line"} series={[{ "points": [{ "label": t("s081"), "value": 1.2, "display": "¥1.20" }, { "label": t("s082"), "value": 0.8, "display": "¥0.80" }, { "label": t("s083"), "value": 2.4, "display": "¥2.40" }, { "label": t("s084"), "value": 1.6, "display": "¥1.60" }, { "label": t("s085"), "value": 3.1, "display": "¥3.10" }, { "label": t("s086"), "value": 2.2, "display": "¥2.20" }, { "label": t("s087"), "value": 0.5, "display": "¥0.50" }] }]} />
      </Section>
      <Section key="chart-section-3" title={t("s093")}>
        <Chart key="chart-chart-item-3-0" kind={"bar"} series={[{ "name": t("s094"), "tone": "danger", "points": [{ "label": t("s081"), "value": 5 }, { "label": t("s082"), "value": -3 }, { "label": t("s083"), "value": 2 }, { "label": t("s084"), "value": -8 }, { "label": t("s085"), "value": 4 }, { "label": t("s086"), "value": 0 }, { "label": t("s087"), "value": -1 }] }]} />
      </Section>
    </>;
    case "stat_grid": return <>
      <Section key="stat_grid-section-0" title={t("s095")}>
        <StatGrid key="stat_grid-stat_grid-item-0-0" stats={[{ "label": t("s096"), "value": "1.2M", "detail": t("s097"), "tone": "caution" }]} />
      </Section>
      <Section key="stat_grid-section-1" title={t("s098")}>
        <StatGrid key="stat_grid-stat_grid-item-1-0" stats={[{ "label": t("s096"), "value": "1.2M", "detail": t("s097"), "tone": "caution" }, { "label": t("s079"), "value": "8.4M" }]} />
      </Section>
      <Section key="stat_grid-section-2" title={t("s099")}>
        <StatGrid key="stat_grid-stat_grid-item-2-0" stats={[{ "label": t("s096"), "value": "1.2M", "detail": t("s097"), "tone": "caution" }, { "label": t("s079"), "value": "8.4M" }, { "label": t("s092"), "value": "¥36.20", "detail": t("s100"), "tone": "positive" }]} />
      </Section>
      <Section key="stat_grid-section-3" title={t("s101")}>
        <StatGrid key="stat_grid-stat_grid-item-3-0" stats={[{ "label": t("s096"), "value": "1.2M", "detail": t("s097"), "tone": "caution" }, { "label": t("s079"), "value": "8.4M" }, { "label": t("s092"), "value": "¥36.20", "detail": t("s100"), "tone": "positive" }, { "label": t("s102"), "value": "3", "detail": t("s103"), "tone": "danger" }]} />
      </Section>
      <Section key="stat_grid-section-4" title={t("s104")}>
        <StatGrid key="stat_grid-stat_grid-item-4-0" stats={[{ "label": t("s096"), "value": "1.2M", "detail": t("s097"), "tone": "caution" }, { "label": t("s079"), "value": "8.4M" }, { "label": t("s092"), "value": "¥36.20", "detail": t("s100"), "tone": "positive" }, { "label": t("s102"), "value": "3", "detail": t("s103"), "tone": "danger" }, { "label": t("s105"), "value": "12,034", "detail": t("s106"), "tone": "neutral" }]} />
      </Section>
      <Section key="stat_grid-section-5" title={t("s107")}>
        <StatGrid key="stat_grid-stat_grid-item-5-0" stats={[{ "label": t("s096"), "value": "1.2M", "detail": t("s097"), "tone": "caution" }, { "label": t("s079"), "value": "8.4M" }, { "label": t("s092"), "value": "¥36.20", "detail": t("s100"), "tone": "positive" }, { "label": t("s102"), "value": "3", "detail": t("s103"), "tone": "danger" }, { "label": t("s105"), "value": "12,034", "detail": t("s106"), "tone": "neutral" }, { "label": t("s108"), "value": "999,999,999" }]} />
      </Section>
    </>;
    case "markdown": return <>
      <Section key="markdown-section-0" >
        <Markdown key="markdown-markdown-item-0-0" text={t("s109")} />
      </Section>
    </>;
    case "table": return <>
      <Section key="table-section-0" title={t("s110")}>
        <Table key="table-table-item-0-0" columns={[{ "title": t("s111") }, { "title": t("s112"), "align": "end" }]} rows={[["11-01", "12"], ["11-02", "8"], ["11-03", "1,024"]]} caption={t("s113")} />
      </Section>
      <Section key="table-section-1" title={t("s114")}>
        <Table key="table-table-item-1-0" columns={[{ "title": t("s111") }, { "title": t("s115"), "align": "end" }, { "title": t("s116"), "align": "end" }, { "title": t("s117"), "align": "end" }, { "title": t("s118"), "align": "end" }, { "title": t("s119"), "align": "end" }, { "title": t("s120"), "align": "end" }, { "title": t("s121") }]} rows={[["10-30", "2,000", "3,011", "4,022", "5,033", "6,044", "7,055", t("s122")], ["10-31", "4,037", "6,048", "8,059", "10,070", "12,081", "14,092", t("s122")], ["11-01", "6,074", "9,085", "12,096", "15,107", "18,118", "21,129", t("s122")], ["11-02", "8,111", "12,122", "16,133", "20,144", "24,155", "28,166", t("s122")], ["11-03", "10,148", "15,159", "20,170", "25,181", "30,192", "35,203", t("s122")], ["11-04", "12,185", "18,196", "24,207", "30,218", "36,229", "42,240", t("s122")], ["11-05", "14,222", "21,233", "28,244", "35,255", "42,266", "49,277", t("s122")]]} />
      </Section>
    </>;
    case "empty": return <>
      <Section key="empty-section-0" title={t("s123")}>
        <Empty key="empty-empty-item-0-0" detail={t("s124")} title={t("s125")} icon={"folder"} button={{ title: t("s126"), action: act("toast") }} />
      </Section>
      <Section key="empty-section-1" title={t("s127")}>
        <Empty key="empty-empty-item-1-0" title={t("s128")} />
      </Section>
    </>;
    case "loading": return <>
      <Section key="loading-section-0" title={t("s129")}>
        <Loading key="loading-loading-item-0-0" title={t("s130")} />
      </Section>
      <Section key="loading-section-1" title={t("s131")}>
        <Loading key="loading-loading-item-1-0"  />
      </Section>
    </>;
    case "divider": return <>
      <Section key="divider-section-0" title={t("s132")} footer={t("s133")}>
        <Row key="divider-divider-item-0-0" title={t("s134")} />
        <Row key="divider-divider-item-0-1" title={t("s135")} />
        <Divider key="divider-divider-item-0-2"  />
        <Row key="divider-divider-item-0-3" title={t("s136")} />
        <Divider key="divider-divider-item-0-4"  />
        <Text key="divider-divider-item-0-5" text={t("s137")} />
      </Section>
    </>;
    default: return null;
  }
}
