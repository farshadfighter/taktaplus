import { useEffect, useState } from 'react'
import { Alert, Button, Card, Descriptions, Input, Popconfirm, Select, Space, Spin, Table, Tabs, Tag, Typography, App as AntApp } from 'antd'
import { Link, useNavigate, useParams } from 'react-router'
import { api, getText, type Backup, type Change, type RestoreJob } from '../api'
import { useCan } from '../auth'
import { saveText } from '../download'
import { faDateTime, faNum } from '../format'
import { Mono, PageHeader, bytesText, useReauth } from '../ui'
import { useFetch } from '../useFetch'
import { reasonLabel } from './Backups'

const kindTag = (k: string) => (k === 'added' ? <Tag color="green">اضافه</Tag> : k === 'removed' ? <Tag color="red">حذف</Tag> : <Tag color="gold">تغییر</Tag>)

export function ChangeTable({ changes, loading }: { changes: Change[] | null; loading?: boolean }) {
  if (loading) return <Spin />
  if (!changes) return null
  if (changes.length === 0) return <Alert type="success" showIcon title="تفاوتی وجود ندارد." />
  return (
    <Table<Change> rowKey={(c, i) => `${c.path}|${c.key}|${i}`} size="small" dataSource={changes} pagination={{ pageSize: 50, hideOnSinglePage: true }} scroll={{ x: 800 }}
      columns={[
        { title: 'نوع', render: (_, c) => kindTag(c.kind), width: 80 },
        { title: 'بلاک', render: (_, c) => <Mono>{c.path || '(سطح بالا)'}</Mono> },
        { title: 'قبل', render: (_, c) => (c.old ? <Mono><span style={{ color: '#CF1322' }}>{c.old}</span></Mono> : c.kind === 'removed' && !c.key ? <Typography.Text type="secondary">کل بلاک</Typography.Text> : '') },
        { title: 'بعد', render: (_, c) => (c.new ? <Mono><span style={{ color: '#389E0D' }}>{c.new}</span></Mono> : c.kind === 'added' && !c.key ? <Typography.Text type="secondary">کل بلاک</Typography.Text> : '') },
      ]} />
  )
}

export default function BackupView() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { message } = AntApp.useApp()
  const canAdmin = useCan('backups.admin')
  const canRestore = useCan('backups.restore')
  const { data, error, loading, reload } = useFetch<{ backup: Backup; content: string }>(`/backups/${id}/content`)
  const b = data?.backup
  const siblings = useFetch<{ items: Backup[] }>(b ? `/backups?device=${b.device_id}` : null)
  const [compareTo, setCompareTo] = useState<number | null>(null)
  const [diff, setDiff] = useState<{ changes: Change[] | null; loading: boolean }>({ changes: null, loading: false })
  const [preview, setPreview] = useState<{ changes: Change[] | null; loading: boolean; error?: string }>({ changes: null, loading: false })
  const [note, setNote] = useState('')
  const [job, setJob] = useState<RestoreJob | null>(null)
  const reauth = useReauth()

  useEffect(() => setNote(b?.note ?? ''), [b])
  useEffect(() => {
    if (!compareTo || !b) return
    const [from, to] = compareTo < b.id ? [compareTo, b.id] : [b.id, compareTo]
    setDiff({ changes: null, loading: true })
    api.get<{ changes: Change[] }>(`/backups/diff?from=${from}&to=${to}`)
      .then((r) => setDiff({ changes: r.changes, loading: false }))
      .catch((e) => { message.error(e.message); setDiff({ changes: null, loading: false }) })
  }, [compareTo, b, message])
  // Follow a running restore until it finishes.
  useEffect(() => {
    if (!job || job.status === 'succeeded' || job.status === 'failed') return
    const t = setTimeout(() => { api.get<RestoreJob>(`/restores/${job.id}`).then(setJob).catch(() => {}) }, 5000)
    return () => clearTimeout(t)
  }, [job])

  if (loading && !data) return <Spin />
  if (error) return <Alert type="error" title={error} showIcon />
  if (!b || !data) return null

  const loadPreview = async () => {
    setPreview({ changes: null, loading: true })
    try {
      const r = await api.get<{ changes: Change[] }>(`/backups/${id}/diff-current`)
      setPreview({ changes: r.changes, loading: false })
    } catch (e) {
      setPreview({ changes: null, loading: false, error: (e as Error).message })
    }
  }
  const download = async () => {
    try {
      saveText(`${b.device_name}-${b.at.slice(0, 16).replace(/[:T]/g, '')}.conf`, await getText(`/backups/${id}/download`))
    } catch (e) {
      message.error((e as Error).message)
    }
  }
  const setLock = async (locked: boolean) => {
    try {
      await api.put(`/backups/${id}`, { locked, note })
      message.success('ذخیره شد')
      void reload()
    } catch (e) {
      message.error((e as Error).message)
    }
  }
  const restore = () =>
    reauth.ask(`ریستور ${b.device_name}`, 'ریستور و ریبوت', async (v) => {
      setJob(await api.post<RestoreJob>(`/backups/${id}/restore`, v))
    }, (
      <Alert type="warning" showIcon style={{ marginBottom: 12 }} title="دستگاه پس از ریستور ریبوت می‌شود."
        description="پیش از ریستور یک بکاپ ایمنی از کانفیگ فعلی گرفته می‌شود. ریستور از طریق REST API انجام می‌شود." />
    ))

  return (
    <>
      <Typography.Text type="secondary"><Link to="/backups">بکاپ‌ها</Link> / <Link to={`/devices/${b.device_id}`}>{b.device_name}</Link></Typography.Text>
      <PageHeader title={<Space>بکاپ {faDateTime(b.at)}{b.locked && <Tag color="purple">قفل</Tag>}</Space>}
        extra={
          <>
            {canAdmin && <Button onClick={() => void download()}>دانلود فایل کامل</Button>}
            {canRestore && <Button type="primary" danger onClick={restore}>ریستور روی دستگاه</Button>}
            {canAdmin && !b.locked && (
              <Popconfirm title="این بکاپ حذف شود؟" okText="حذف" cancelText="انصراف"
                onConfirm={async () => { try { await api.del(`/backups/${id}`); navigate('/backups') } catch (e) { message.error((e as Error).message) } }}>
                <Button danger>حذف</Button>
              </Popconfirm>
            )}
          </>
        } />
      {job && (
        <Alert style={{ marginBottom: 16 }} showIcon
          type={job.status === 'succeeded' ? 'success' : job.status === 'failed' ? 'error' : 'info'}
          title={job.status === 'succeeded' ? 'ریستور انجام شد و دستگاه دوباره در دسترس است.' : job.status === 'failed' ? `ریستور ناموفق: ${job.error}` : 'کانفیگ ارسال شد؛ در انتظار بالا آمدن دستگاه…'}
          description={job.pre_backup_id && <span>بکاپ ایمنی: <Link to={`/backups/${job.pre_backup_id}`}>#{faNum(job.pre_backup_id)}</Link></span>} />
      )}
      <Card style={{ marginBottom: 16 }}>
        <Descriptions size="small" column={{ xs: 1, md: 3 }}>
          <Descriptions.Item label="دستگاه"><Link to={`/devices/${b.device_id}`}>{b.device_name}</Link></Descriptions.Item>
          <Descriptions.Item label="علت">{reasonLabel[b.reason]}</Descriptions.Item>
          <Descriptions.Item label="توسط">{b.actor || '—'}</Descriptions.Item>
          <Descriptions.Item label="فرم‌ور">{b.firmware ? <Mono>v{b.firmware}</Mono> : '—'}</Descriptions.Item>
          <Descriptions.Item label="حجم"><Mono>{bytesText(b.size)}</Mono></Descriptions.Item>
          <Descriptions.Item label="SHA-256"><Mono>{b.sha256.slice(0, 16)}…</Mono></Descriptions.Item>
        </Descriptions>
        {canAdmin && (
          <Space.Compact style={{ width: '100%', maxWidth: 560, marginTop: 8 }}>
            <Input placeholder="یادداشت (مثلاً: قبل از ارتقا)" value={note} onChange={(e) => setNote(e.target.value)} />
            <Button onClick={() => void setLock(b.locked)}>ذخیره یادداشت</Button>
            <Button onClick={() => void setLock(!b.locked)}>{b.locked ? 'باز کردن قفل' : 'قفل (عدم حذف خودکار)'}</Button>
          </Space.Compact>
        )}
      </Card>
      <Tabs items={[
        {
          key: 'content', label: 'محتوا',
          children: (
            <Card>
              <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>رمزها و مقادیر ENC در این نمایش پوشانده شده‌اند.</Typography.Paragraph>
              <pre dir="ltr" style={{ maxHeight: 600, overflow: 'auto', background: '#FAFAFA', border: '1px solid #EEE', borderRadius: 8, padding: 12, fontSize: 12, margin: 0 }}>{data.content}</pre>
            </Card>
          ),
        },
        {
          key: 'diff', label: 'مقایسه با بکاپ دیگر',
          children: (
            <Card>
              <Select style={{ width: 320, marginBottom: 16 }} placeholder="انتخاب بکاپ برای مقایسه" value={compareTo} onChange={setCompareTo}
                options={(siblings.data?.items ?? []).filter((x) => x.status === 'ok' && x.id !== b.id).map((x) => ({ value: x.id, label: `${faDateTime(x.at)} · ${reasonLabel[x.reason]}` }))} />
              <ChangeTable changes={diff.changes} loading={diff.loading} />
            </Card>
          ),
        },
        ...(canRestore ? [{
          key: 'preview', label: 'پیش‌نمایش ریستور',
          children: (
            <Card>
              <Typography.Paragraph type="secondary">کانفیگ فعلی دستگاه خوانده و با این بکاپ مقایسه می‌شود؛ جدول نشان می‌دهد ریستور چه چیزی را تغییر می‌دهد.</Typography.Paragraph>
              <Button onClick={() => void loadPreview()} loading={preview.loading} style={{ marginBottom: 16 }}>مقایسه با کانفیگ فعلی دستگاه</Button>
              {preview.error && <Alert type="error" showIcon title={preview.error} />}
              <ChangeTable changes={preview.changes} />
            </Card>
          ),
        }] : []),
      ]} />
      {reauth.modal}
    </>
  )
}
