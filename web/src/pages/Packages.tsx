import { useEffect, useState } from 'react'
import { Alert, Button, Card, Col, DatePicker, Form, Input, InputNumber, Modal, Popconfirm, Progress, Radio, Row, Select, Space, Switch, Table, Tabs, Tag, Typography, Upload, App as AntApp } from 'antd'
import dayjs from 'dayjs'
import { api, type PackageSource, type RepoPackage, type SyncRun } from '../api'
import { useCan } from '../auth'
import { faDate, faDateTime, faNum } from '../format'
import { Mono, PageHeader, bytesText } from '../ui'
import { useFetch } from '../useFetch'

export const typeLabel: Record<string, string> = {
  ips_db: 'پایگاه IPS', ips_engine: 'موتور IPS', av_db: 'پایگاه آنتی‌ویروس', av_engine: 'موتور آنتی‌ویروس', appctrl_db: 'کنترل برنامه',
  botnet_db: 'Botnet', isdb: 'Internet Service DB', ot_db: 'OT', waf_signature: 'امضای WAF', ip_reputation: 'اعتبار IP', geo_db: 'Geo IP', firmware: 'فرم‌ور', geoip_mmdb: 'پایگاه کشورها (TaktaPlus)',
}
const types: Record<string, string[]> = {
  fortigate: ['ips_db', 'ips_engine', 'av_db', 'av_engine', 'appctrl_db', 'botnet_db', 'isdb', 'ot_db', 'firmware'],
  fortiweb: ['waf_signature', 'av_db', 'ip_reputation', 'geo_db', 'firmware'],
}
const sourceKinds: Record<string, string> = { central: 'مخزن مرکزی تکتاپلاس', http: 'آینه‌ی HTTP(S)', ftp: 'FTP', sftp: 'SFTP', tftp: 'TFTP' }

// upload sends a file with optional JSON meta and reports progress.
function upload(path: string, file: File, meta: object | null, onProgress: (p: number) => void): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const body = new FormData()
    if (meta) body.append('meta', JSON.stringify(meta))
    body.append('file', file)
    const xhr = new XMLHttpRequest()
    xhr.open('POST', '/api/v1' + path)
    xhr.setRequestHeader('X-Requested-With', 'taktaplus')
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress(Math.round((e.loaded / e.total) * 100))
    xhr.onload = () => {
      let data: { error?: string } = {}
      try { data = JSON.parse(xhr.responseText) } catch { /* not JSON */ }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data)
      else reject(new Error(data.error ?? xhr.statusText))
    }
    xhr.onerror = () => reject(new Error('ارسال فایل ناموفق بود'))
    xhr.send(body)
  })
}

function EditPackage({ pkg, onClose }: { pkg: RepoPackage | null; onClose: (saved: boolean) => void }) {
  const [form] = Form.useForm()
  const platform = Form.useWatch('platform', form) ?? 'fortigate'
  const type = Form.useWatch('type', form)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    if (pkg) {
      setError(null)
      form.setFieldsValue({ ...pkg, released_at: dayjs(pkg.released_at) })
    }
  }, [pkg, form])
  return (
    <Modal open={pkg !== null} title={`ویرایش مشخصات ${pkg?.file ?? ''}`} okText="ذخیره" cancelText="انصراف" width={620} onCancel={() => onClose(false)} onOk={() => form.submit()}>
      <Typography.Paragraph type="secondary">این فایل Manifest امضاشده ندارد؛ مشخصات از نام فایل حدس زده شده و باید درست باشد تا بررسی سازگاری صحیح کار کند.</Typography.Paragraph>
      {error && <Alert type="error" showIcon title={error} style={{ marginBottom: 12 }} />}
      <Form form={form} layout="vertical" requiredMark={false} onFinish={async (v) => {
        try {
          await api.put(`/packages/${pkg!.id}`, { ...v, models: v.models ?? [], upgrade_from: v.upgrade_from ?? [], fw_min: v.fw_min ?? '', fw_max: v.fw_max ?? '', notes: v.notes ?? '', released_at: (v.released_at ?? dayjs()).toISOString() })
          onClose(true)
        } catch (e) { setError((e as Error).message) }
      }}>
        <Row gutter={12}>
          <Col span={12}><Form.Item name="platform" label="پلتفرم"><Radio.Group optionType="button" options={[{ value: 'fortigate', label: 'FortiGate' }, { value: 'fortiweb', label: 'FortiWeb' }]} /></Form.Item></Col>
          <Col span={12}><Form.Item name="type" label="نوع"><Select options={(types[platform] ?? []).map((t) => ({ value: t, label: typeLabel[t] }))} /></Form.Item></Col>
          <Col span={12}><Form.Item name="version" label="نسخه" rules={[{ required: true }]}><Input dir="ltr" /></Form.Item></Col>
          <Col span={12}><Form.Item name="released_at" label="تاریخ انتشار"><DatePicker style={{ width: '100%' }} /></Form.Item></Col>
          <Col span={24}><Form.Item name="models" label="مدل‌های سازگار (خالی = همه)"><Select mode="tags" tokenSeparators={[',', ' ']} /></Form.Item></Col>
          <Col span={12}><Form.Item name="fw_min" label="حداقل فرم‌ور"><Input dir="ltr" /></Form.Item></Col>
          <Col span={12}><Form.Item name="fw_max" label="حداکثر فرم‌ور"><Input dir="ltr" /></Form.Item></Col>
          {type === 'firmware' && <Col span={24}><Form.Item name="upgrade_from" label="ارتقای مستقیم از"><Select mode="tags" tokenSeparators={[',', ' ']} /></Form.Item></Col>}
        </Row>
      </Form>
    </Modal>
  )
}

function SourceModal({ source, onClose }: { source: Partial<PackageSource> | null; onClose: (saved: boolean) => void }) {
  const [form] = Form.useForm()
  const kind = Form.useWatch('kind', form)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    if (source) {
      setError(null)
      form.resetFields()
      form.setFieldsValue({ kind: 'central', enabled: true, schedule: '15 2 * * *', ...source, config: { ...source.config }, files: source.config?.files?.join('\n') })
    }
  }, [source, form])
  return (
    <Modal open={source !== null} title={source?.id ? 'ویرایش منبع' : 'منبع پکیج جدید'} okText="ذخیره" cancelText="انصراف" width={600} onCancel={() => onClose(false)} onOk={() => form.submit()}>
      {error && <Alert type="error" showIcon title={error} style={{ marginBottom: 12 }} />}
      <Form form={form} layout="vertical" requiredMark={false} onFinish={async (v) => {
        const config = { ...v.config, files: v.files ? String(v.files).split(/\s+/).filter(Boolean) : undefined }
        const body = { name: v.name, kind: v.kind, schedule: v.schedule ?? '', enabled: v.enabled, config }
        try {
          if (source?.id) await api.put(`/package-sources/${source.id}`, body)
          else await api.post('/package-sources', body)
          onClose(true)
        } catch (e) { setError((e as Error).message) }
      }}>
        <Form.Item name="name" label="نام" rules={[{ required: true }]}><Input /></Form.Item>
        <Form.Item name="kind" label="نوع"><Select disabled={!!source?.id} options={Object.entries(sourceKinds).map(([value, label]) => ({ value, label }))} /></Form.Item>
        {kind === 'central' && (
          <Form.Item name={['config', 'url']} label="آدرس سرور لایسنس (اختیاری)" extra="خالی: همان سرور لایسنسی که این نصب با آن فعال شده است. احراز هویت با کلید نصب انجام می‌شود.">
            <Input dir="ltr" placeholder="https://license.example.com" />
          </Form.Item>
        )}
        {kind === 'http' && (
          <Form.Item name={['config', 'url']} label="آدرس آینه" rules={[{ required: true }]} extra="شامل manifest.json، manifest.sig و پوشه‌ی files/ (مثل محتوای بسته‌ی آفلاین)">
            <Input dir="ltr" placeholder="https://mirror.example.com/taktaplus" />
          </Form.Item>
        )}
        {(kind === 'ftp' || kind === 'sftp' || kind === 'tftp') && (
          <>
            <Space.Compact block>
              <Form.Item name={['config', 'host']} label="سرور" style={{ flex: 1 }} rules={[{ required: true }]}><Input dir="ltr" /></Form.Item>
              <Form.Item name={['config', 'port']} label="پورت"><InputNumber min={1} max={65535} placeholder={kind === 'ftp' ? '21' : kind === 'sftp' ? '22' : '69'} /></Form.Item>
            </Space.Compact>
            {kind !== 'tftp' && (
              <>
                <Space.Compact block>
                  <Form.Item name={['config', 'user']} label="نام کاربری" style={{ flex: 1 }}><Input dir="ltr" autoComplete="off" /></Form.Item>
                  <Form.Item name={['config', 'password']} label="رمز" style={{ flex: 1 }}><Input.Password dir="ltr" autoComplete="new-password" /></Form.Item>
                </Space.Compact>
                <Form.Item name={['config', 'dir']} label="پوشه" extra="اگر پوشه manifest.json و manifest.sig داشته باشد، امضای آن بررسی می‌شود؛ وگرنه همه‌ی فایل‌ها بدون امضا وارد می‌شوند.">
                  <Input dir="ltr" placeholder="/fortiguard" />
                </Form.Item>
              </>
            )}
            {kind === 'ftp' && <Form.Item name={['config', 'tls']} label="FTP روی TLS" valuePropName="checked"><Switch /></Form.Item>}
            {kind === 'tftp' && <Form.Item name="files" label="نام فایل‌ها (هر خط یکی)" rules={[{ required: true }]} extra="TFTP فهرست فایل ندارد."><Input.TextArea dir="ltr" rows={3} /></Form.Item>}
          </>
        )}
        <Space.Compact block>
          <Form.Item name="schedule" label="زمان‌بندی همگام‌سازی (Cron، خالی = دستی)" style={{ flex: 1 }}><Input dir="ltr" /></Form.Item>
          <Form.Item name="enabled" label="فعال" valuePropName="checked"><Switch /></Form.Item>
        </Space.Compact>
      </Form>
    </Modal>
  )
}

export default function Packages() {
  const { message } = AntApp.useApp()
  const canManage = useCan('packages.manage')
  const [platform, setPlatform] = useState('')
  const pkgs = useFetch<{ items: RepoPackage[] }>('/packages?all=1&platform=' + platform)
  const sources = useFetch<{ items: PackageSource[]; runs: SyncRun[] }>('/package-sources')
  const [editing, setEditing] = useState<RepoPackage | null>(null)
  const [source, setSource] = useState<Partial<PackageSource> | null>(null)
  const [progress, setProgress] = useState<number | null>(null)
  // After "sync" is clicked, follow the runs until one started after the
  // click has finished (the sync itself runs in the background).
  const [syncSince, setSyncSince] = useState<number | null>(null)
  const runs = sources.data?.runs ?? []
  const following = syncSince !== null && !runs.some((r) => r.status !== 'running' && new Date(r.started_at).getTime() >= syncSince - 2000)
  const running = runs.some((r) => r.status === 'running')

  useEffect(() => {
    if (!following && !running) {
      if (syncSince !== null) {
        setSyncSince(null)
        void pkgs.reload()
      }
      return
    }
    const t = setInterval(() => { void sources.reload(); void pkgs.reload() }, 2000)
    return () => clearInterval(t)
  }, [following, running, syncSince, sources, pkgs])

  const send = async (path: string, file: File, ok: (r: unknown) => string) => {
    setProgress(0)
    try {
      const r = await upload(path, file, null, setProgress)
      message.success(ok(r))
      void pkgs.reload()
      void sources.reload()
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setProgress(null)
    }
  }
  const sync = async (s: PackageSource) => {
    try {
      setSyncSince(Date.now())
      await api.post(`/package-sources/${s.id}/sync`)
      message.info('همگام‌سازی شروع شد')
    } catch (e) {
      message.error((e as Error).message)
    }
  }
  const items = pkgs.data?.items ?? []

  return (
    <>
      <PageHeader title="پکیج‌ها و فرم‌ور" subtitle={`${faNum(items.filter((p) => !p.withdrawn).length)} فایل در مخزن داخلی`}
        extra={canManage && (
          <>
            <Upload accept=".zip" showUploadList={false} beforeUpload={(f) => { void send('/packages/bundle', f, (r) => `بسته وارد شد: ${faNum((r as SyncRun).added)} فایل جدید`); return false }}>
              <Button disabled={progress !== null}>بارگذاری بسته‌ی آفلاین</Button>
            </Upload>
            <Upload showUploadList={false} beforeUpload={(f) => { void send('/packages/upload', f, () => 'فایل بارگذاری شد؛ مشخصات حدس‌زده‌شده را بررسی کنید'); return false }}>
              <Button disabled={progress !== null}>بارگذاری فایل</Button>
            </Upload>
          </>
        )} />
      {progress !== null && <Progress percent={progress} style={{ marginBottom: 16 }} />}
      <Tabs items={[
        {
          key: 'repo', label: 'مخزن', children: (
            <Card>
              <Radio.Group optionType="button" value={platform} onChange={(e) => setPlatform(e.target.value)} style={{ marginBottom: 16 }}
                options={[{ value: '', label: 'همه' }, { value: 'fortigate', label: 'FortiGate' }, { value: 'fortiweb', label: 'FortiWeb' }]} />
              <Table<RepoPackage> rowKey="id" size="small" loading={pkgs.loading} dataSource={items} scroll={{ x: 1000 }} pagination={{ pageSize: 30, hideOnSinglePage: true }}
                locale={{ emptyText: 'مخزن خالی است؛ یک منبع تعریف و همگام کنید یا فایل بارگذاری کنید.' }}
                columns={[
                  { title: 'فایل', render: (_, p) => <div><Mono>{p.file}</Mono><div><Typography.Text type="secondary" style={{ fontSize: 12 }}>{bytesText(p.size)} · {p.origin || '—'}</Typography.Text></div></div> },
                  { title: 'نوع', render: (_, p) => `${p.platform === 'fortigate' ? 'FortiGate' : 'FortiWeb'} · ${typeLabel[p.type] ?? p.type}` },
                  { title: 'نسخه', render: (_, p) => <Mono>{p.version}</Mono> },
                  { title: 'سازگاری', render: (_, p) => <span style={{ fontSize: 12 }}>{p.models.join('، ')}{(p.fw_min || p.fw_max) && <> · <Mono>{p.fw_min || '…'}–{p.fw_max || '…'}</Mono></>}{p.upgrade_from.length > 0 && <> · از <Mono>{p.upgrade_from.join(', ')}</Mono></>}</span> },
                  { title: 'انتشار', render: (_, p) => faDate(p.released_at) },
                  { title: 'وضعیت', render: (_, p) => <Space size={4}>{p.verified ? <Tag color="green">امضاشده</Tag> : <Tag color="gold">بدون امضا</Tag>}{p.withdrawn && <Tag color="red">حذف از منبع</Tag>}</Space> },
                  ...(canManage ? [{
                    title: '', render: (_: unknown, p: RepoPackage) => (
                      <Space size={0}>
                        {!p.verified && <Button size="small" type="link" onClick={() => setEditing(p)}>مشخصات</Button>}
                        <Popconfirm title="فایل از مخزن داخلی حذف شود؟" okText="حذف" cancelText="انصراف"
                          onConfirm={async () => { await api.del(`/packages/${p.id}`).catch((e) => message.error(e.message)); void pkgs.reload() }}>
                          <Button size="small" type="link" danger>حذف</Button>
                        </Popconfirm>
                      </Space>
                    ),
                  }] : []),
                ]} />
            </Card>
          ),
        },
        {
          key: 'sources', label: 'منابع', children: (
            <Card extra={canManage && <Button type="primary" onClick={() => setSource({})}>+ منبع جدید</Button>}>
              <Table<PackageSource> rowKey="id" size="small" pagination={false} dataSource={sources.data?.items ?? []} scroll={{ x: 900 }}
                locale={{ emptyText: 'منبعی تعریف نشده' }}
                columns={[
                  { title: 'نام', dataIndex: 'name' },
                  { title: 'نوع', render: (_, s) => sourceKinds[s.kind] },
                  { title: 'نشانی', render: (_, s) => <Mono>{s.config.url || s.config.host || '—'}</Mono> },
                  { title: 'زمان‌بندی', render: (_, s) => (s.schedule ? <span><Mono>{s.schedule}</Mono>{s.next_sync_at && <div style={{ fontSize: 12, color: '#888' }}>بعدی {faDateTime(s.next_sync_at)}</div>}</span> : 'دستی') },
                  { title: 'آخرین همگام‌سازی', render: (_, s) => (s.last_sync_at ? <span>{faDateTime(s.last_sync_at)} {s.last_status === 'ok' ? <Tag color="green">موفق</Tag> : <Tag color="red">ناموفق</Tag>}{s.last_error && <div style={{ fontSize: 12, color: '#CF1322' }}>{s.last_error}</div>}</span> : '—') },
                  ...(canManage ? [{
                    title: '', render: (_: unknown, s: PackageSource) => (
                      <Space size={0}>
                        <Button size="small" type="link" onClick={() => void sync(s)}>همگام‌سازی</Button>
                        <Button size="small" type="link" onClick={() => setSource(s)}>ویرایش</Button>
                        <Popconfirm title="منبع حذف شود؟ فایل‌های دریافت‌شده می‌مانند." okText="حذف" cancelText="انصراف"
                          onConfirm={async () => { await api.del(`/package-sources/${s.id}`).catch((e) => message.error(e.message)); void sources.reload() }}>
                          <Button size="small" type="link" danger>حذف</Button>
                        </Popconfirm>
                      </Space>
                    ),
                  }] : []),
                ]} />
              <Typography.Title level={5} style={{ marginTop: 24 }}>همگام‌سازی‌های اخیر</Typography.Title>
              <Table<SyncRun> rowKey="id" size="small" pagination={{ pageSize: 10, hideOnSinglePage: true }} dataSource={sources.data?.runs ?? []}
                columns={[
                  { title: 'زمان', render: (_, r) => faDateTime(r.started_at) },
                  { title: 'منبع', dataIndex: 'origin' },
                  { title: 'نتیجه', render: (_, r) => (r.status === 'running' ? <Tag color="processing">در حال اجرا</Tag> : r.status === 'ok' ? <Tag color="green">موفق</Tag> : <Tag color="red">ناموفق</Tag>) },
                  { title: 'فایل', render: (_, r) => `${faNum(r.added)} جدید از ${faNum(r.seen)}` },
                  { title: 'خطا', render: (_, r) => r.error && <Typography.Text type="danger" style={{ fontSize: 12 }}>{r.error}</Typography.Text> },
                  { title: 'توسط', dataIndex: 'actor' },
                ]} />
            </Card>
          ),
        },
      ]} />
      <EditPackage pkg={editing} onClose={(s) => { setEditing(null); if (s) void pkgs.reload() }} />
      <SourceModal source={source} onClose={(s) => { setSource(null); if (s) void sources.reload() }} />
    </>
  )
}
