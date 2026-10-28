import { useEffect, useState } from 'react'
import { Alert, Button, Col, Row, Spin, Tabs, Typography } from 'antd'
import { useNavigate, useSearchParams } from 'react-router'
import { api, type LogStatus, type LogWidget, type ReportTemplate } from '../api'
import { useCan } from '../auth'
import { faNum } from '../format'
import { DeviceSelect, PeriodPicker, WidgetView, periodOf, rangeQuery, useDevices, type Period } from '../logui'
import { PageHeader } from '../ui'
import { useFetch } from '../useFetch'

const tabs: [string, string, string][] = [
  ['security', 'خلاصه‌ی امنیتی', 'خلاصه‌ی امنیتی'],
  ['traffic', 'ترافیک', 'مصرف پهنای باند'],
  ['web', 'WebFilter', 'WebFilter'],
  ['vpn', 'VPN', 'فعالیت VPN'],
  ['waf', 'حملات WAF (FortiWeb)', 'حملات WAF'],
  ['events', 'رویدادها', 'رویدادهای سیستمی'],
]

export default function Analytics() {
  const nav = useNavigate()
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') ?? 'security'
  const [period, setPeriod] = useState<Period>(() => periodOf('24h'))
  const [devs, setDevs] = useState<number[]>([])
  const { list, names } = useDevices()
  const status = useFetch<LogStatus>('/logs/status')
  const templates = useFetch<{ items: ReportTemplate[] }>('/reports/templates')
  const canReport = useCan('logs.read')
  const [widgets, setWidgets] = useState<LogWidget[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    let live = true
    setLoading(true)
    api
      .get<{ widgets: LogWidget[] }>(`/logs/dashboard?tab=${tab}&${rangeQuery(period, devs)}`)
      .then((r) => { if (live) { setWidgets(r.widgets); setError(null) } })
      .catch((e) => { if (live) setError(e.message) })
      .finally(() => { if (live) setLoading(false) })
    return () => { live = false }
  }, [tab, period, devs])

  const drill = (w: LogWidget, row: Record<string, string | number>) => {
    if (!w.drill) return
    const q = new URLSearchParams({ type: w.drill.type, from: period.from, to: period.to, f: JSON.stringify([{ field: w.drill.field, op: 'eq', value: String(row[w.drill.key]) }]) })
    if (devs.length) q.set('devices', devs.join(','))
    nav('/logs?' + q.toString())
  }

  const st = status.data
  const tpl = templates.data?.items.find((t) => t.builtin && t.name === tabs.find((t) => t[0] === tab)?.[2])
  const subtitle = st?.enabled && st.stats
    ? `دریافت لاگ: ${faNum(Math.round(st.stats.eps))} EPS · ${faNum(st.stats.devices.length)} دستگاه ارسال‌کننده · نگهداری ${faNum(st.settings.retention.raw_days)} روز`
    : undefined

  const kpis = widgets?.filter((w) => w.kind === 'kpis') ?? []
  const series = widgets?.filter((w) => w.kind === 'series') ?? []
  const rest = widgets?.filter((w) => w.kind === 'bars' || w.kind === 'table') ?? []

  return (
    <>
      <PageHeader
        title="تحلیل امنیتی"
        subtitle={subtitle}
        extra={
          <>
            <PeriodPicker value={period} onChange={setPeriod} />
            <DeviceSelect value={devs} onChange={setDevs} devices={list} />
            {canReport && tpl && (
              <Button onClick={() => nav(`/reports?run=${tpl.id}&from=${encodeURIComponent(period.from)}&to=${encodeURIComponent(period.to)}${devs.length ? '&devices=' + devs.join(',') : ''}`)}>
                ساخت گزارش از این نما
              </Button>
            )}
          </>
        }
      />
      {st && !st.enabled && <Alert type="warning" showIcon style={{ marginBottom: 16 }} title="ذخیره‌ساز لاگ پیکربندی نشده است" description="برای فعال‌سازی، TAKTAPLUS_CLICKHOUSE_URL را تنظیم کنید." />}
      <Tabs activeKey={tab} onChange={(k) => setParams({ tab: k })} items={tabs.map(([k, label]) => ({ key: k, label }))} />
      {error && <Alert type="error" showIcon title={error} style={{ marginBottom: 16 }} />}
      <Spin spinning={loading && widgets !== null}>
        {widgets === null && loading ? (
          <div style={{ padding: 48, textAlign: 'center' }}><Spin /></div>
        ) : (
          <Row gutter={[16, 16]}>
            {kpis.map((w) => (
              <Col span={24} key={w.id}><WidgetView w={w} /></Col>
            ))}
            {series.map((w) => (
              <Col span={24} key={w.id}><WidgetView w={w} /></Col>
            ))}
            {rest.map((w) => (
              <Col xs={24} xl={12} key={w.id}><WidgetView w={w} devices={names} onDrill={drill} /></Col>
            ))}
          </Row>
        )}
      </Spin>
      {widgets && rest.some((w) => w.drill) && (
        <Typography.Paragraph type="secondary" style={{ marginTop: 12, fontSize: 12 }}>برای دیدن لاگ‌های هر ردیف، روی آن کلیک کنید.</Typography.Paragraph>
      )}
    </>
  )
}
