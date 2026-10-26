import { Alert, Card, Col, Row, Statistic, Table, Typography } from 'antd'
import { Link } from 'react-router'
import type { AlertRow, Backup, DeviceRow, LicenseInfo } from '../api'
import { useAuth, can } from '../auth'
import { daysUntil, faDate, faDateTime, faNum } from '../format'
import { brand } from '../theme'
import { PageHeader, Usage, severityTag, statusTag } from '../ui'
import { useFetch } from '../useFetch'
import { reasonLabel } from './Backups'

export default function Dashboard() {
  const { user } = useAuth()
  const devices = useFetch<{ items: DeviceRow[] }>('/devices')
  const alerts = useFetch<{ items: AlertRow[]; open: Record<string, number> }>('/alerts?state=open&limit=8')
  const backups = useFetch<{ items: Backup[] }>('/backups?limit=8')
  const lic = useFetch<LicenseInfo>(can(user, 'license.read') ? '/license' : null)

  const list = devices.data?.items ?? []
  const online = list.filter((d) => d.status === 'online').length
  const down = list.filter((d) => d.status === 'offline' || d.status === 'error').length
  const unlicensed = list.filter((d) => d.license !== 'active' && d.license !== 'grace').length
  const open = alerts.data?.open ?? {}
  const busiest = [...list].filter((d) => d.health).sort((a, b) => (b.health?.cpu ?? 0) - (a.health?.cpu ?? 0)).slice(0, 5)
  const notAfter = lic.data?.license?.not_after
  const left = daysUntil(notAfter)
  const nextExpiry = lic.data?.license?.items.filter((i) => i.kind === 'device').map((i) => i.expires).sort()[0]

  return (
    <>
      <PageHeader title="داشبورد" subtitle={`خوش آمدید، ${user?.name}`} />
      {lic.data && !lic.data.license && (
        <Alert type="warning" showIcon style={{ marginBottom: 16 }} title="لایسنسی نصب نشده است" description={<Link to="/license">فعال‌سازی لایسنس</Link>} />
      )}
      {lic.data?.clock_rolled_back && <Alert type="error" showIcon style={{ marginBottom: 16 }} title="ساعت سیستم عقب کشیده شده است؛ بررسی لایسنس با آخرین زمان ثبت‌شده انجام می‌شود." />}
      {left !== null && left <= 30 && <Alert type="warning" showIcon style={{ marginBottom: 16 }} title={`اعتبار فایل لایسنس ${faNum(Math.max(left, 0))} روز دیگر تمام می‌شود.`} />}
      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        <Col xs={12} lg={6}><Card><Statistic title="تجهیزات" value={faNum(list.length)} suffix={<Typography.Text type="secondary" style={{ fontSize: 13 }}>{faNum(online)} آنلاین</Typography.Text>} /></Card></Col>
        <Col xs={12} lg={6}><Card><Statistic title="قطع ارتباط" value={faNum(down)} valueStyle={{ color: down ? '#CF1322' : undefined }} /></Card></Col>
        <Col xs={12} lg={6}><Card><Statistic title="هشدار باز" value={faNum((open.critical ?? 0) + (open.warning ?? 0) + (open.info ?? 0))} valueStyle={{ color: open.critical ? '#CF1322' : brand.primary }}
          suffix={open.critical ? <Typography.Text type="danger" style={{ fontSize: 13 }}>{faNum(open.critical)} بحرانی</Typography.Text> : null} /></Card></Col>
        <Col xs={12} lg={6}><Card><Statistic title="نزدیک‌ترین انقضای لایسنس" value={nextExpiry ? faDate(nextExpiry) : '—'}
          suffix={unlicensed ? <Typography.Text type="warning" style={{ fontSize: 13 }}>{faNum(unlicensed)} بدون لایسنس</Typography.Text> : null} /></Card></Col>
      </Row>
      <Row gutter={[16, 16]}>
        <Col xs={24} xl={12}>
          <Card title="هشدارهای باز" extra={<Link to="/alerts">همه</Link>}>
            <Table<AlertRow> rowKey="id" size="small" pagination={false} dataSource={alerts.data?.items ?? []} locale={{ emptyText: 'هشدار بازی نیست' }}
              columns={[
                { title: '', render: (_, a) => severityTag(a.severity), width: 70 },
                { title: 'دستگاه', render: (_, a) => <Link to={`/devices/${a.device_id}`}>{a.device_name}</Link> },
                { title: 'پیام', dataIndex: 'message', ellipsis: true },
                { title: 'از', render: (_, a) => faDateTime(a.opened_at) },
              ]} />
          </Card>
        </Col>
        <Col xs={24} xl={12}>
          <Card title="بیشترین بار CPU" extra={<Link to="/devices">تجهیزات</Link>}>
            <Table<DeviceRow> rowKey="id" size="small" pagination={false} dataSource={busiest} locale={{ emptyText: 'داده‌ای نیست' }}
              columns={[
                { title: 'دستگاه', render: (_, d) => <Link to={`/devices/${d.id}`}>{d.name}</Link> },
                { title: 'وضعیت', render: (_, d) => statusTag(d.status) },
                { title: 'CPU', render: (_, d) => <Usage value={d.health?.cpu} /> },
                { title: 'حافظه', render: (_, d) => <Usage value={d.health?.mem} /> },
              ]} />
          </Card>
        </Col>
        <Col xs={24}>
          <Card title="آخرین بکاپ‌ها" extra={<Link to="/backups">همه</Link>}>
            <Table<Backup> rowKey="id" size="small" pagination={false} dataSource={backups.data?.items ?? []} locale={{ emptyText: 'بکاپی ثبت نشده' }} scroll={{ x: 600 }}
              columns={[
                { title: 'زمان', render: (_, b) => (b.status === 'ok' ? <Link to={`/backups/${b.id}`}>{faDateTime(b.at)}</Link> : faDateTime(b.at)) },
                { title: 'دستگاه', dataIndex: 'device_name' },
                { title: 'علت', render: (_, b) => reasonLabel[b.reason] },
                { title: 'نتیجه', render: (_, b) => (b.status === 'ok' ? 'موفق' : <Typography.Text type="danger">{b.error}</Typography.Text>) },
              ]} />
          </Card>
        </Col>
      </Row>
    </>
  )
}
