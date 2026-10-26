import { useState } from 'react'
import { Alert, Button, Card, Col, Descriptions, Empty, Radio, Row, Space, Spin, Table, Tabs, Tag, Typography, App as AntApp } from 'antd'
import { Link, useNavigate, useParams } from 'react-router'
import { api, type AlertRow, type Backup, type Contract, type Device, type DeviceStatus, type Iface, type Metric } from '../api'
import { useCan } from '../auth'
import { LineChart, chartColors } from '../chart'
import { daysUntil, faDate, faDateTime, faNum } from '../format'
import { Mono, PageHeader, Usage, bytesText, kindLabel, licenseTag, severityTag, statusTag, uptimeText, useReauth } from '../ui'
import { useFetch } from '../useFetch'
import { BackupTable } from './Backups'

function contractTag(c: Contract) {
  const d = daysUntil(c.expires)
  if (c.status !== 'licensed' && c.status !== 'valid') return <Tag color="red">{c.status}</Tag>
  if (d !== null && d < 0) return <Tag color="red">منقضی</Tag>
  if (d !== null && d <= 30) return <Tag color="gold">{faNum(d)} روز مانده</Tag>
  return <Tag color="green">معتبر</Tag>
}

function Monitoring({ id }: { id: string }) {
  const [hours, setHours] = useState(24)
  const { data, loading } = useFetch<{ items: Metric[] }>(`/devices/${id}/metrics?hours=${hours}`)
  const items = data?.items ?? []
  return (
    <Card title="روند منابع" extra={
      <Radio.Group size="small" optionType="button" value={hours} onChange={(e) => setHours(e.target.value)}
        options={[{ value: 6, label: '۶ ساعت' }, { value: 24, label: '۲۴ ساعت' }, { value: 168, label: '۷ روز' }, { value: 720, label: '۳۰ روز' }]} />
    }>
      {loading ? <Spin /> : (
        <LineChart times={items.map((m) => m.at)} series={[
          { label: 'CPU', color: chartColors.cpu, values: items.map((m) => m.cpu) },
          { label: 'حافظه', color: chartColors.mem, values: items.map((m) => m.mem) },
          ...(items.some((m) => m.disk >= 0) ? [{ label: 'دیسک', color: chartColors.disk, values: items.map((m) => m.disk) }] : []),
        ]} />
      )}
    </Card>
  )
}

function Overview({ d, st }: { d: Device; st: DeviceStatus | null }) {
  return (
    <Row gutter={[16, 16]}>
      <Col xs={24} lg={12}>
        <Card title="مشخصات">
          <Descriptions column={1} size="small">
            <Descriptions.Item label="نوع و مدل">{kindLabel(d.kind)} {d.model}</Descriptions.Item>
            <Descriptions.Item label="سریال">{d.serial ? <Mono>{d.serial}</Mono> : '—'}</Descriptions.Item>
            <Descriptions.Item label="فرم‌ور">{d.firmware ? <Mono>v{d.firmware} build{d.build}</Mono> : '—'}</Descriptions.Item>
            <Descriptions.Item label="Hostname">{d.hostname || '—'}</Descriptions.Item>
            <Descriptions.Item label="آدرس مدیریت"><Mono>{d.host}:{d.https_port}</Mono></Descriptions.Item>
            <Descriptions.Item label="اتصال">
              <Space size={4}>{d.has_api && <Tag>REST API</Tag>}{d.has_ssh && <Tag>SSH</Tag>}</Space>
            </Descriptions.Item>
            <Descriptions.Item label="HA">{d.ha_mode || '—'}</Descriptions.Item>
            {d.ha_peers.map((p) => (
              <Descriptions.Item key={p.serial} label="عضو HA"><Mono>{p.serial}</Mono> {licenseTag(p.license)}</Descriptions.Item>
            ))}
            {d.vdoms.length > 0 && <Descriptions.Item label="VDOMها">{d.vdoms.join('، ')}</Descriptions.Item>}
            <Descriptions.Item label="Uptime">{st ? uptimeText(st.uptime) : '—'}</Descriptions.Item>
            <Descriptions.Item label="گروه">{d.group_name || '—'}</Descriptions.Item>
            {d.tags.length > 0 && <Descriptions.Item label="برچسب‌ها">{d.tags.map((t) => <Tag key={t}>{t}</Tag>)}</Descriptions.Item>}
            <Descriptions.Item label="FortiManager">{d.fortimanager ? 'متصل' : 'غیرفعال'}</Descriptions.Item>
            <Descriptions.Item label="آخرین شناسایی">{faDateTime(d.last_probe_at)}</Descriptions.Item>
          </Descriptions>
        </Card>
      </Col>
      <Col xs={24} lg={12}>
        <Card title="قراردادهای FortiGuard روی دستگاه">
          <Table<Contract> rowKey="name" size="small" pagination={false} dataSource={st?.contracts ?? []}
            locale={{ emptyText: 'اطلاعاتی دریافت نشده' }}
            columns={[
              { title: 'سرویس', dataIndex: 'name', render: (v: string) => <Mono>{v}</Mono> },
              { title: 'انقضا', dataIndex: 'expires', render: (v: string | null) => faDate(v) },
              { title: 'وضعیت', render: (_, c) => contractTag(c) },
            ]} />
        </Card>
      </Col>
      <Col xs={24}>
        <Card title="اینترفیس‌ها">
          <Table<Iface> rowKey="name" size="small" pagination={false} scroll={{ x: 600 }} dataSource={st?.interfaces ?? []}
            locale={{ emptyText: 'اطلاعاتی دریافت نشده' }}
            columns={[
              { title: 'نام', render: (_, i) => <span><Mono>{i.name}</Mono>{i.alias && <Typography.Text type="secondary"> · {i.alias}</Typography.Text>}</span> },
              { title: 'آدرس', render: (_, i) => (i.ip ? <Mono>{i.ip}</Mono> : '—') },
              { title: 'وضعیت', render: (_, i) => (i.up ? <Tag color="green">Up</Tag> : <Tag>Down</Tag>) },
              { title: 'سرعت', render: (_, i) => (i.speed ? `${faNum(i.speed)} Mbps` : '—') },
              { title: 'دریافت', render: (_, i) => <Mono>{bytesText(i.rx_bytes)}</Mono> },
              { title: 'ارسال', render: (_, i) => <Mono>{bytesText(i.tx_bytes)}</Mono> },
            ]} />
        </Card>
      </Col>
    </Row>
  )
}

export default function DeviceDetail() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { message } = AntApp.useApp()
  const canWrite = useCan('devices.write')
  const canBackup = useCan('backups.run')
  const { data, error, loading, reload, setData } = useFetch<{ device: Device; status: DeviceStatus | null }>(`/devices/${id}`)
  const backups = useFetch<{ items: Backup[] }>(`/backups?device=${id}`)
  const alerts = useFetch<{ items: AlertRow[] }>(`/alerts?device=${id}`)
  const [busy, setBusy] = useState<string | null>(null)
  const reauth = useReauth()

  if (loading && !data) return <Spin />
  if (error && !data) return <Alert type="error" title={error} showIcon />
  if (!data) return null
  const { device: d, status: st } = data
  const operational = d.license === 'active' || d.license === 'grace'

  const refresh = async () => {
    setBusy('refresh')
    try {
      setData(await api.post(`/devices/${id}/refresh`))
      message.success('اطلاعات دستگاه به‌روز شد')
    } catch (e) {
      message.error((e as Error).message)
      void reload()
    } finally {
      setBusy(null)
    }
  }
  const backupNow = async () => {
    setBusy('backup')
    try {
      const res = await api.post<{ unchanged: boolean }>(`/devices/${id}/backup`)
      message.success(res.unchanged ? 'کانفیگ نسبت به آخرین بکاپ تغییری نکرده است' : 'بکاپ گرفته شد')
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setBusy(null)
      void backups.reload()
    }
  }

  return (
    <>
      <Typography.Text type="secondary"><Link to="/devices">تجهیزات</Link> / {d.name}</Typography.Text>
      <PageHeader
        title={<Space wrap>{d.name}{statusTag(d.status)}{d.ha_mode && d.ha_mode !== 'standalone' && <Tag>HA · {d.ha_mode}</Tag>}{licenseTag(d.license)}</Space>}
        extra={
          <>
            <Button onClick={() => void refresh()} loading={busy === 'refresh'}>به‌روزرسانی</Button>
            {canBackup && <Button type="primary" disabled={!operational} onClick={() => void backupNow()} loading={busy === 'backup'}>بکاپ فوری</Button>}
            {canWrite && <Button onClick={() => navigate(`/devices/${id}/edit`)}>ویرایش</Button>}
            {canWrite && (
              <Button danger onClick={() => reauth.ask(`حذف ${d.name}`, 'حذف دستگاه', async (v) => {
                await api.del(`/devices/${id}`, v)
                message.success('دستگاه حذف شد')
                navigate('/devices')
              }, <Alert type="warning" showIcon style={{ marginBottom: 12 }} title="وضعیت، تاریخچه‌ی پایش، هشدارها و بکاپ‌های این دستگاه هم حذف می‌شوند." />)}>حذف</Button>
            )}
          </>
        }
      />
      {d.last_error && d.status !== 'online' && <Alert type="error" showIcon style={{ marginBottom: 16 }} title="آخرین خطای اتصال" description={<Mono>{d.last_error}</Mono>} />}
      {!operational && (
        <Alert type="warning" showIcon style={{ marginBottom: 16 }}
          title={d.license === 'expired' ? 'لایسنس این سریال منقضی شده؛ فقط مشاهده و دانلود داده‌های قبلی ممکن است.' : 'این سریال در لایسنس نیست؛ پایش و بکاپ انجام نمی‌شود.'} />
      )}
      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        {[
          { label: 'CPU', node: <Usage value={st?.cpu} /> },
          { label: 'حافظه', node: <Usage value={st?.mem} /> },
          { label: 'دیسک', node: <Usage value={st?.disk} /> },
          { label: 'Session', node: <Typography.Title level={4} style={{ margin: 0 }}>{st ? faNum(st.sessions) : '—'}</Typography.Title> },
        ].map((g) => (
          <Col xs={12} md={6} key={g.label}>
            <Card size="small"><Typography.Text type="secondary">{g.label}</Typography.Text><div style={{ marginTop: 6 }}>{g.node}</div></Card>
          </Col>
        ))}
      </Row>
      <Tabs
        items={[
          { key: 'overview', label: 'نمای کلی', children: <Overview d={d} st={st} /> },
          { key: 'monitor', label: 'پایش', children: <Monitoring id={id!} /> },
          {
            key: 'signatures', label: 'سیگنیچرها',
            children: (
              <Card extra={st?.signatures_at && <Typography.Text type="secondary">به‌روز شده {faDateTime(st.signatures_at)}</Typography.Text>}>
                {(st?.signatures ?? []).length === 0 ? <Empty description="اطلاعاتی دریافت نشده" /> : (
                  <Table rowKey="name" size="small" pagination={false} dataSource={st?.signatures ?? []}
                    columns={[
                      { title: 'پایگاه داده / موتور', dataIndex: 'name', render: (v: string) => <Mono>{v}</Mono> },
                      { title: 'نسخه‌ی نصب‌شده', dataIndex: 'version', render: (v: string) => <Mono>{v}</Mono> },
                    ]} />
                )}
              </Card>
            ),
          },
          { key: 'backups', label: 'بکاپ‌ها', children: <Card><BackupTable items={backups.data?.items ?? []} loading={backups.loading} hideDevice /></Card> },
          {
            key: 'alerts', label: 'هشدارها',
            children: (
              <Card>
                <Table<AlertRow> rowKey="id" size="small" dataSource={alerts.data?.items ?? []} pagination={{ pageSize: 20, hideOnSinglePage: true }}
                  locale={{ emptyText: 'هشداری ثبت نشده' }}
                  columns={[
                    { title: 'شدت', render: (_, a) => severityTag(a.severity) },
                    { title: 'قانون', dataIndex: 'rule_name' },
                    { title: 'پیام', dataIndex: 'message' },
                    { title: 'وضعیت', render: (_, a) => (a.state === 'open' ? <Tag color="red">باز</Tag> : <Tag>رفع‌شده</Tag>) },
                    { title: 'زمان', render: (_, a) => faDateTime(a.opened_at) },
                  ]} />
              </Card>
            ),
          },
        ]}
      />
      {reauth.modal}
    </>
  )
}
