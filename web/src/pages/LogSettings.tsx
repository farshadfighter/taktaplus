import { useEffect, useState } from 'react'
import { Alert, Button, Card, Col, Descriptions, Form, Input, InputNumber, Radio, Row, Space, Statistic, Table, Tag, Typography, Upload, App as AntApp } from 'antd'
import { useNavigate } from 'react-router'
import { api, type LogStatus } from '../api'
import { useCan } from '../auth'
import { faDateTime, faNum } from '../format'
import { Mono, PageHeader, bytesText } from '../ui'
import { useFetch } from '../useFetch'

const dropLabel: Record<string, string> = {
  unparsable: 'قالب نامعتبر', unknown_device: 'دستگاه ثبت‌نشده', unlicensed: 'بدون لایسنس', untrusted_source: 'آدرس مبدأ نامعتبر', buffer_full: 'ذخیره‌ساز در دسترس نبود',
}
const tableLabel: Record<string, string> = {
  log_traffic: 'ترافیک', log_threat: 'تهدید و WebFilter', log_event: 'رویداد و VPN', log_waf: 'حملات WAF',
}

function SyslogCLI({ server }: { server: string }) {
  const { message } = AntApp.useApp()
  const nav = useNavigate()
  const canScript = useCan('changes.create')
  const [cfg, setCfg] = useState({ server, protocol: 'tcp', port: 514 })
  const [cli, setCli] = useState<{ fortigate: string; fortiweb: string } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [platform, setPlatform] = useState<'fortigate' | 'fortiweb'>('fortigate')
  useEffect(() => setCfg((c) => ({ ...c, server: c.server || server })), [server])
  useEffect(() => {
    if (!cfg.server) return
    api.post<{ fortigate: string; fortiweb: string }>('/logs/syslog-config', cfg).then((r) => { setCli(r); setError(null) }).catch((e) => { setCli(null); setError(e.message) })
  }, [cfg])
  const text = cli?.[platform] ?? ''
  return (
    <Card title="پیکربندی ارسال لاگ روی دستگاه‌ها" size="small">
      <Space wrap style={{ marginBottom: 12 }}>
        <Input addonBefore="آدرس TaktaPlus" dir="ltr" value={cfg.server} onChange={(e) => setCfg({ ...cfg, server: e.target.value })} style={{ width: 260 }} />
        <Radio.Group optionType="button" value={cfg.protocol} onChange={(e) => setCfg({ ...cfg, protocol: e.target.value, port: e.target.value === 'tls' ? 6514 : 514 })}
          options={[{ value: 'udp', label: 'UDP' }, { value: 'tcp', label: 'TCP' }, { value: 'tls', label: 'TLS' }]} />
        <InputNumber addonBefore="پورت" min={1} max={65535} value={cfg.port} onChange={(v) => setCfg({ ...cfg, port: Number(v) || 514 })} />
        <Radio.Group value={platform} onChange={(e) => setPlatform(e.target.value)} options={[{ value: 'fortigate', label: 'FortiGate' }, { value: 'fortiweb', label: 'FortiWeb' }]} />
      </Space>
      {error && <Alert type="warning" showIcon title={error} />}
      {text && (
        <Typography.Paragraph copyable={{ text }} style={{ margin: 0 }}>
          <pre dir="ltr" style={{ background: '#FAFAFA', border: '1px solid #EEE', borderRadius: 6, padding: 10, fontSize: 12, margin: 0 }}>{text}</pre>
        </Typography.Paragraph>
      )}
      <Typography.Paragraph type="secondary" style={{ marginTop: 8, fontSize: 12 }}>
        TCP با قاب‌بندی RFC 6587 (mode reliable) پیشنهاد می‌شود. این تنظیمات را می‌توانید به صورت اسکریپت ذخیره کنید و از طریق گردش کار تغییرات روی دستگاه‌ها اعمال کنید.
      </Typography.Paragraph>
      {canScript && text && (
        <Button onClick={async () => {
          try {
            const r = await api.post<{ id: number }>('/logs/syslog-script', { ...cfg, platform })
            message.success('اسکریپت ذخیره شد')
            nav(`/scripts/${r.id}`)
          } catch (e) {
            message.error((e as Error).message)
          }
        }}>ذخیره به عنوان اسکریپت و اعمال</Button>
      )}
    </Card>
  )
}

export default function LogSettings() {
  const { message } = AntApp.useApp()
  const canManage = useCan('logs.manage')
  const { data, reload, error } = useFetch<LogStatus>('/logs/status')
  const [form] = Form.useForm()
  useEffect(() => {
    if (data) form.setFieldsValue({ ...data.settings.retention, trusted_sources: data.settings.trusted_sources.join('\n') })
  }, [data, form])
  useEffect(() => {
    const t = setInterval(() => void reload(), 10_000)
    return () => clearInterval(t)
  }, [reload])

  const save = async (patch: { retention?: { raw_days: number; rollup_days: number }; trusted_sources?: string[] }) => {
    if (!data) return
    try {
      await api.put('/logs/settings', { ...data.settings, ...patch })
      message.success('ذخیره شد')
      await reload()
    } catch (e) {
      message.error((e as Error).message)
    }
  }
  const st = data?.stats
  const listeners = Object.entries(st?.listeners ?? {})

  return (
    <>
      <PageHeader title="تنظیمات لاگ" subtitle="دریافت Syslog، نگهداری، منابع مجاز و پایگاه کشورها"
        extra={listeners.map(([p, a]) => <Tag key={p}>{p.toUpperCase()} <Mono>{a}</Mono></Tag>)} />
      {error && <Alert type="error" showIcon title={error} style={{ marginBottom: 16 }} />}
      {data && !data.enabled && <Alert type="warning" showIcon style={{ marginBottom: 16 }} title="ذخیره‌ساز لاگ پیکربندی نشده است" description="TAKTAPLUS_CLICKHOUSE_URL را تنظیم و سرویس را دوباره راه‌اندازی کنید." />}
      {data?.store_error && <Alert type="error" showIcon style={{ marginBottom: 16 }} title="ذخیره‌ساز لاگ در دسترس نیست" description={data.store_error} />}
      {st && (
        <>
          <Row gutter={[12, 12]} style={{ marginBottom: 16 }}>
            <Col xs={12} md={6}><Card size="small"><Statistic title="نرخ دریافت (EPS)" value={faNum(Math.round(st.eps * 10) / 10)} /></Card></Col>
            <Col xs={12} md={6}><Card size="small"><Statistic title="دریافت‌شده" value={faNum(st.received)} /></Card></Col>
            <Col xs={12} md={6}><Card size="small"><Statistic title="ذخیره‌شده" value={faNum(st.accepted)} /></Card></Col>
            <Col xs={12} md={6}><Card size="small"><Statistic title="دورریخته" value={faNum(Object.values(st.dropped).reduce((a, b) => a + b, 0))} /></Card></Col>
          </Row>
          {st.last_error && <Alert type="error" showIcon title="خطای نوشتن در ذخیره‌ساز" description={st.last_error} style={{ marginBottom: 16 }} />}
          <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
            <Col xs={24} xl={12}>
              <Card title="دستگاه‌های ارسال‌کننده" size="small" styles={{ body: { padding: 0 } }}>
                <Table size="small" rowKey="device_id" pagination={false} dataSource={st.devices} locale={{ emptyText: 'هنوز لاگی دریافت نشده است' }}
                  columns={[
                    { title: 'دستگاه', dataIndex: 'name' },
                    { title: 'آدرس مبدأ', dataIndex: 'source_ip', render: (v: string) => <Mono>{v}</Mono> },
                    { title: 'تعداد', dataIndex: 'count', render: (v: number) => faNum(v) },
                    { title: 'آخرین', dataIndex: 'last_at', render: (v: string) => faDateTime(v) },
                  ]} />
              </Card>
            </Col>
            <Col xs={24} xl={12}>
              <Card title="لاگ‌های دورریخته" size="small" styles={{ body: { padding: 0 } }}
                extra={<Space wrap>{Object.entries(st.dropped).map(([k, v]) => <Tag key={k}>{dropLabel[k] ?? k}: {faNum(v)}</Tag>)}</Space>}>
                <Table size="small" rowKey={(r) => r.serial + r.ip + r.reason} pagination={false} dataSource={st.rejected} locale={{ emptyText: 'موردی نیست' }}
                  columns={[
                    { title: 'سریال', dataIndex: 'serial', render: (v: string) => <Mono>{v}</Mono> },
                    { title: 'آدرس', dataIndex: 'ip', render: (v: string) => <Mono>{v}</Mono> },
                    { title: 'علت', dataIndex: 'reason', render: (v: string) => dropLabel[v] ?? v },
                    { title: 'تعداد', dataIndex: 'count', render: (v: number) => faNum(v) },
                    {
                      title: '', key: 'a',
                      render: (_, r) => r.reason === 'untrusted_source' && canManage && data && (
                        <Button size="small" type="link" onClick={() => save({ trusted_sources: [...data.settings.trusted_sources, r.ip] })}>اعتماد به این آدرس</Button>
                      ),
                    },
                  ]} />
              </Card>
            </Col>
          </Row>
        </>
      )}
      <Row gutter={[16, 16]}>
        <Col xs={24} xl={12}>
          <Card title="نگهداری و منابع مجاز" size="small">
            <Form form={form} layout="vertical" requiredMark={false} disabled={!canManage} onFinish={(v) => save({
              retention: { raw_days: v.raw_days, rollup_days: v.rollup_days },
              trusted_sources: String(v.trusted_sources ?? '').split(/[\s,]+/).filter(Boolean),
            })}>
              <Row gutter={12}>
                <Col span={12}><Form.Item name="raw_days" label="لاگ خام (روز)" rules={[{ required: true }]}><InputNumber min={1} max={3650} style={{ width: '100%' }} /></Form.Item></Col>
                <Col span={12}><Form.Item name="rollup_days" label="خلاصه‌های ساعتی (روز)" rules={[{ required: true }]}><InputNumber min={1} max={3650} style={{ width: '100%' }} /></Form.Item></Col>
              </Row>
              <Form.Item name="trusted_sources" label="آدرس‌ها و شبکه‌های مجاز اضافه"
                extra="لاگ هر دستگاه از آدرس ثبت‌شده‌ی خودش پذیرفته می‌شود. اگر دستگاه از اینترفیس دیگری لاگ می‌فرستد، آدرس یا شبکه‌ی آن را اینجا وارد کنید (هر خط یک مورد).">
                <Input.TextArea rows={3} dir="ltr" placeholder="10.10.0.0/24" />
              </Form.Item>
              <Button type="primary" htmlType="submit">ذخیره</Button>
            </Form>
            {data?.usage && data.usage.length > 0 && (
              <Table size="small" rowKey="table" style={{ marginTop: 16 }} pagination={false} dataSource={data.usage.filter((u) => u.table.startsWith('log_'))}
                columns={[
                  { title: 'جدول', dataIndex: 'table', render: (v: string) => tableLabel[v] ?? v },
                  { title: 'رکورد', dataIndex: 'rows', render: (v: number) => faNum(v) },
                  { title: 'حجم', dataIndex: 'bytes', render: (v: number) => <span dir="ltr">{bytesText(v)}</span> },
                ]}
                summary={() => (
                  <Table.Summary.Row>
                    <Table.Summary.Cell index={0}>کل (با خلاصه‌ها)</Table.Summary.Cell>
                    <Table.Summary.Cell index={1}>—</Table.Summary.Cell>
                    <Table.Summary.Cell index={2}><span dir="ltr">{bytesText(data.usage!.reduce((a, u) => a + u.bytes, 0))}</span></Table.Summary.Cell>
                  </Table.Summary.Row>
                )} />
            )}
          </Card>
        </Col>
        <Col xs={24} xl={12}>
          <Card title="پایگاه کشورها (GeoIP)" size="small" style={{ marginBottom: 16 }}>
            <Descriptions size="small" column={1} items={[
              { key: 's', label: 'وضعیت', children: data?.geoip.loaded ? <Tag color="green">نصب شده</Tag> : <Tag>نصب نشده</Tag> },
              ...(data?.geoip.loaded ? [
                { key: 't', label: 'نوع', children: <Mono>{data.geoip.type}</Mono> },
                { key: 'b', label: 'تاریخ ساخت', children: faDateTime(data.geoip.built_at) },
                { key: 'src', label: 'منبع', children: data.geoip.source || '—' },
              ] : []),
            ]} />
            <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
              کشور مبدأ و مقصد از خود لاگ فورتی‌گیت خوانده می‌شود؛ برای آدرس‌هایی که دستگاه کشورشان را نفرستاده (مثل FortiWeb) از این پایگاه استفاده می‌شود.
              نسخه‌ی جدید از مخزن مرکزی پکیج‌ها (نوع geoip_mmdb) خودکار نصب می‌شود؛ می‌توانید فایل MMDB (مثل DB-IP Lite Country) را دستی هم بارگذاری کنید.
            </Typography.Paragraph>
            {canManage && (
              <Upload accept=".mmdb" showUploadList={false} action="/api/v1/logs/geoip" headers={{ 'X-Requested-With': 'taktaplus' }} name="file"
                onChange={(info) => {
                  if (info.file.status === 'done') { message.success('پایگاه کشورها نصب شد'); void reload() }
                  if (info.file.status === 'error') message.error(info.file.response?.error ?? 'بارگذاری ناموفق بود')
                }}>
                <Button>بارگذاری فایل MMDB</Button>
              </Upload>
            )}
          </Card>
          <SyslogCLI server={data?.syslog_server ?? ''} />
        </Col>
      </Row>
    </>
  )
}
