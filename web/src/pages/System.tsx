import { useEffect, useState } from 'react'
import { Alert, Button, Card, Col, Collapse, Descriptions, Form, Input, InputNumber, Modal, Progress, Row, Select, Space, Switch, Table, Tag, Typography, Upload, App as AntApp } from 'antd'
import { api, type Channel, type SystemBackupView, type SystemInfo, type SystemRun, type SystemUpgrade } from '../api'
import { faDateTime, faNum } from '../format'
import { upload } from '../upload'
import { bytesText, Mono, PageHeader, useReauth } from '../ui'
import { useLiveRefresh } from '../live'
import { useFetch } from '../useFetch'

type Overview = { info: SystemInfo; settings: SystemBackupView; runs: SystemRun[] | null; upgrades: SystemUpgrade[] | null }

const cronPresets = [
  { value: '0 3 * * *', label: 'روزانه ۰۳:۰۰' },
  { value: '0 3 * * 5', label: 'هفتگی، جمعه ۰۳:۰۰' },
  { value: '0 */6 * * *', label: 'هر ۶ ساعت' },
]
const triggerLabel: Record<string, string> = { manual: 'دستی', schedule: 'زمان‌بندی', pre_upgrade: 'پیش از ارتقا', file: 'فایل موجود' }
const upgradeTag: Record<string, [string, string]> = {
  staged: ['آماده', 'default'], queued: ['در صف', 'gold'], running: ['در حال اجرا', 'processing'], done: ['انجام شد', 'green'],
  failed: ['ناموفق', 'red'], rolled_back: ['برگشت به نسخه‌ی قبل', 'orange'], cancelled: ['لغو شد', 'default'],
}
const stepLabel: Record<string, string> = {
  snapshot: 'نسخه‌ی پشتیبان دیتابیس', backup: 'بکاپ رمزنگاری‌شده', load: 'بارگذاری ایمیج‌ها', switch: 'تعویض نسخه',
  start: 'راه‌اندازی', health: 'بررسی سلامت', rollback: 'بازگشت', done: 'پایان',
}

function PassphraseModal({ open, onClose, onDone }: { open: boolean; onClose: () => void; onDone: () => void }) {
  const { message } = AntApp.useApp()
  const [form] = Form.useForm()
  const [busy, setBusy] = useState(false)
  return (
    <Modal open={open} title="عبارت عبور بکاپ" okText="ذخیره" okButtonProps={{ loading: busy }} onCancel={onClose} onOk={() => form.submit()} destroyOnHidden>
      <Alert type="warning" showIcon style={{ marginBottom: 12 }} title="این عبارت عبور ذخیره نمی‌شود"
        description="بازیابی بکاپ روی سرور تازه فقط با فایل بکاپ و همین عبارت عبور ممکن است. آن را جای امنی (مثلاً گاوصندوق رمز سازمان) نگه دارید. بکاپ‌های قبلی با عبارت عبور قبلی باز می‌شوند." />
      <Form form={form} layout="vertical" onFinish={async (v) => {
        setBusy(true)
        try {
          await api.post('/system/backup/passphrase', { passphrase: v.passphrase, password: v.password, code: String(v.code).trim() })
          message.success('عبارت عبور بکاپ ذخیره شد')
          onDone()
        } catch (e) {
          message.error((e as Error).message)
        } finally {
          setBusy(false)
        }
      }}>
        <Form.Item name="passphrase" label="عبارت عبور جدید" rules={[{ required: true, min: 12, message: 'حداقل ۱۲ نویسه' }]}><Input.Password dir="ltr" autoComplete="new-password" /></Form.Item>
        <Form.Item name="confirm" label="تکرار" dependencies={['passphrase']}
          rules={[{ required: true, message: 'تکرار را وارد کنید' }, ({ getFieldValue }) => ({ validator: (_, v) => (v === getFieldValue('passphrase') ? Promise.resolve() : Promise.reject(new Error('با عبارت عبور یکسان نیست'))) })]}>
          <Input.Password dir="ltr" autoComplete="new-password" />
        </Form.Item>
        <Typography.Paragraph type="secondary">برای تأیید، رمز ورود و کد Authenticator خود را وارد کنید.</Typography.Paragraph>
        <Row gutter={12}>
          <Col span={14}><Form.Item name="password" label="رمز عبور شما" rules={[{ required: true, message: 'رمز را وارد کنید' }]}><Input.Password dir="ltr" autoComplete="current-password" /></Form.Item></Col>
          <Col span={10}><Form.Item name="code" label="کد ۶ رقمی" rules={[{ required: true, pattern: /^\s*\d{6}\s*$/, message: 'کد ۶ رقمی' }]}><Input dir="ltr" inputMode="numeric" style={{ letterSpacing: 4, textAlign: 'center' }} /></Form.Item></Col>
        </Row>
      </Form>
    </Modal>
  )
}

function BackupCard({ data, channels, onChanged }: { data: Overview; channels: Channel[]; onChanged: () => void }) {
  const { message } = AntApp.useApp()
  const [form] = Form.useForm()
  const [pass, setPass] = useState(false)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const st = data.settings
  const sftpOn = Form.useWatch('sftp_enabled', form)
  const notify = Form.useWatch('notify_channel_id', form)
  const notifyKind = channels.find((c) => c.id === notify)?.kind
  useEffect(() => {
    form.setFieldsValue({ ...st, sftp: { ...st.sftp, password: '', private_key: '' } })
  }, [st, form])
  return (
    <Card title="بکاپ خودکار سیستم" extra={<Space>
      <Button onClick={() => setPass(true)}>{st.key_set ? 'تغییر عبارت عبور' : 'تعیین عبارت عبور'}</Button>
      <Button type="primary" disabled={!st.key_set} onClick={async () => {
        try {
          await api.post('/system/backup/run')
          message.success('بکاپ شروع شد')
          onChanged()
        } catch (e) {
          message.error((e as Error).message)
        }
      }}>بکاپ کامل الان</Button>
    </Space>}>
      {!st.key_set
        ? <Alert type="warning" showIcon style={{ marginBottom: 16 }} title="هنوز عبارت عبور بکاپ تعیین نشده است" description="پیش از هر بکاپ یک عبارت عبور تعیین کنید؛ فایل‌ها با آن رمزنگاری می‌شوند و فقط با آن قابل بازیابی‌اند." />
        : <Typography.Paragraph type="secondary">عبارت عبور در {faDateTime(st.key_set_at)} تعیین شده است. بکاپ شامل دیتابیس، بکاپ دستگاه‌ها، پکیج‌ها، گزارش‌ها، تنظیمات و کلید اصلی است؛ لاگ‌های ClickHouse در آن نیستند.</Typography.Paragraph>}
      <Form form={form} layout="vertical" onFinish={async (v) => {
        setSaving(true)
        try {
          await api.put('/system/backup/settings', { ...v, notify_channel_id: v.notify_channel_id ?? null, notify_to: v.notify_to ?? [],
            sftp: { host: '', port: 22, user: '', dir: '', host_key: '', ...v.sftp } })
          message.success('تنظیمات بکاپ ذخیره شد')
          onChanged()
        } catch (e) {
          message.error((e as Error).message)
        } finally {
          setSaving(false)
        }
      }}>
        <Row gutter={16}>
          <Col xs={24} md={6}><Form.Item name="enabled" label="بکاپ زمان‌بندی‌شده" valuePropName="checked"><Switch /></Form.Item></Col>
          <Col xs={24} md={10}>
            <Form.Item name="cron" label="زمان‌بندی (Cron)" extra={<>پیش‌فرض‌ها: {cronPresets.map((p) => <Button key={p.value} size="small" type="link" onClick={() => form.setFieldValue('cron', p.value)}>{p.label}</Button>)}</>}>
              <Input style={{ direction: 'ltr' }} />
            </Form.Item>
          </Col>
          <Col xs={24} md={8}><Form.Item name="keep" label="تعداد نسخه‌ی محلی"><InputNumber min={1} max={365} style={{ width: '100%' }} /></Form.Item></Col>
        </Row>
        <Form.Item name="sftp_enabled" label="کپی روی سرور SFTP" valuePropName="checked"><Switch /></Form.Item>
        {sftpOn && (
          <Card size="small" style={{ marginBottom: 16, background: '#FAFBFC' }}>
            <Row gutter={12}>
              <Col xs={24} md={10}><Form.Item name={['sftp', 'host']} label="سرور" rules={[{ required: true }]}><Input dir="ltr" /></Form.Item></Col>
              <Col xs={8} md={4}><Form.Item name={['sftp', 'port']} label="پورت"><InputNumber min={1} max={65535} style={{ width: '100%' }} /></Form.Item></Col>
              <Col xs={16} md={10}><Form.Item name={['sftp', 'user']} label="نام کاربری" rules={[{ required: true }]}><Input dir="ltr" /></Form.Item></Col>
              <Col xs={24} md={10}><Form.Item name={['sftp', 'password']} label="رمز"><Input.Password dir="ltr" placeholder={st.has_password ? 'بدون تغییر' : ''} autoComplete="new-password" /></Form.Item></Col>
              <Col xs={24} md={14}><Form.Item name={['sftp', 'dir']} label="پوشه‌ی مقصد" rules={[{ required: true }]}><Input dir="ltr" placeholder="/backups/taktaplus" /></Form.Item></Col>
              <Col span={24}><Form.Item name={['sftp', 'private_key']} label="کلید خصوصی SSH (اختیاری)"><Input.TextArea dir="ltr" rows={2} placeholder={st.has_private_key ? 'بدون تغییر' : '-----BEGIN OPENSSH PRIVATE KEY-----'} /></Form.Item></Col>
              <Col xs={24} md={16}><Form.Item name={['sftp', 'host_key']} label="کلید میزبان (Pin)" extra="با «آزمایش اتصال» خوانده می‌شود؛ اگر تغییر کند ارسال متوقف و هشدار داده می‌شود."><Input dir="ltr" readOnly /></Form.Item></Col>
              <Col xs={24} md={8}><Form.Item name="sftp_keep" label="تعداد نسخه روی SFTP"><InputNumber min={0} max={3650} style={{ width: '100%' }} /></Form.Item></Col>
            </Row>
            <Button loading={testing} onClick={async () => {
              setTesting(true)
              try {
                const r = await api.post<{ ok: boolean; error?: string; host_key: string }>('/system/backup/sftp-test', { port: 22, host_key: '', ...form.getFieldValue('sftp') })
                if (r.host_key) form.setFieldValue(['sftp', 'host_key'], r.host_key)
                if (r.ok) message.success('اتصال و نوشتن در پوشه موفق بود')
                else message.error(r.error ?? 'اتصال ناموفق بود')
              } catch (e) {
                message.error((e as Error).message)
              } finally {
                setTesting(false)
              }
            }}>آزمایش اتصال</Button>
          </Card>
        )}
        <Row gutter={16}>
          <Col xs={24} md={10}>
            <Form.Item name="notify_channel_id" label="اعلان خطا از کانال">
              <Select allowClear placeholder="بدون اعلان" options={channels.map((c) => ({ value: c.id, label: c.name }))} />
            </Form.Item>
          </Col>
          {notifyKind === 'email' && (
            <Col xs={24} md={14}><Form.Item name="notify_to" label="گیرندگان ایمیل"><Select mode="tags" style={{ direction: 'ltr' }} tokenSeparators={[',', ' ']} /></Form.Item></Col>
          )}
        </Row>
        <Button type="primary" htmlType="submit" loading={saving}>ذخیره‌ی تنظیمات بکاپ</Button>
      </Form>
      <PassphraseModal open={pass} onClose={() => setPass(false)} onDone={() => { setPass(false); onChanged() }} />
    </Card>
  )
}

function RunsCard({ runs }: { runs: SystemRun[] }) {
  return (
    <Card title="بکاپ‌های اخیر" styles={{ body: { padding: 0 } }}>
      <Table rowKey={(r) => (r.id ? String(r.id) : r.file)} size="small" dataSource={runs} pagination={{ pageSize: 10, hideOnSinglePage: true }} locale={{ emptyText: 'هنوز بکاپی گرفته نشده است' }}
        columns={[
          { title: 'زمان', dataIndex: 'started_at', render: (v: string) => faDateTime(v) },
          { title: 'نوع', dataIndex: 'trigger', render: (t: string, r) => <>{triggerLabel[t] ?? t}{r.created_by && <Typography.Text type="secondary"> · {r.created_by}</Typography.Text>}</> },
          { title: 'وضعیت', dataIndex: 'status', render: (s: string, r) => (s === 'ok' ? <Tag color="green">موفق</Tag> : s === 'running' ? <Tag color="processing">در حال اجرا</Tag>
            : <Tag color="red" title={r.error}>ناموفق</Tag>) },
          { title: 'حجم', dataIndex: 'size', render: (n: number, r) => (r.status === 'ok' ? bytesText(n) : '—') },
          { title: 'SFTP', dataIndex: 'sftp_status', render: (s: string, r) => (s === 'ok' ? <Tag color="green">ارسال شد</Tag> : s === 'failed' ? <Tag color="red" title={r.error}>ناموفق</Tag> : '—') },
          { title: 'خطا', dataIndex: 'error', ellipsis: true, render: (e: string) => (e ? <Typography.Text type="danger" title={e}>{e}</Typography.Text> : '') },
          { title: '', key: 'dl', render: (_, r) => r.available && <Button size="small" href={`/api/v1/system/backup/file?file=${encodeURIComponent(r.file)}`}>دانلود</Button> },
        ]} />
    </Card>
  )
}

function UpgradeCard({ info, upgrades, onChanged }: { info: SystemInfo; upgrades: SystemUpgrade[]; onChanged: () => void }) {
  const { message } = AntApp.useApp()
  const reauth = useReauth()
  const [progress, setProgress] = useState<number | null>(null)
  const send = (file: File) => reauth.ask('ارتقای TaktaPlus', 'بارگذاری و ارتقا', async ({ password, code }) => {
    setProgress(0)
    try {
      await upload('/system/upgrades', file, { password, code }, setProgress)
      message.success('بسته تأیید شد و در صف ارتقا قرار گرفت')
      onChanged()
    } finally {
      setProgress(null)
    }
  }, <Typography.Paragraph>فایل: <Mono>{file.name}</Mono> ({bytesText(file.size)}). پیش از ارتقا از دیتابیس نسخه‌ی پشتیبان و در صورت تعیین عبارت عبور، بکاپ کامل گرفته می‌شود؛ اگر نسخه‌ی جدید سالم بالا نیاید، نسخه‌ی قبلی و دیتابیس آن برمی‌گردند.</Typography.Paragraph>)
  return (
    <Card title="ارتقای نسخه">
      <Descriptions size="small" column={{ xs: 1, md: 2 }} style={{ marginBottom: 12 }} items={[
        { key: 'v', label: 'نسخه‌ی فعلی', children: <Mono>{info.version}</Mono> },
        { key: 'b', label: 'ساخت', children: <Mono>{info.commit} · {info.build_date}</Mono> },
        { key: 'u', label: 'سرویس ارتقا', children: info.updater ? <Tag color="green">فعال</Tag> : <Tag color="red">پاسخ نمی‌دهد</Tag> },
        { key: 'd', label: 'فضا', children: <>بکاپ‌ها {bytesText(info.backup_bytes)} · فضای آزاد {bytesText(info.free_bytes)}</> },
      ]} />
      {!info.can_verify && <Alert type="error" showIcon style={{ marginBottom: 12 }} title="کلید عمومی انتشار در این نسخه نیست؛ بسته‌های ارتقا قابل تأیید نیستند." />}
      {info.can_verify && !info.updater && <Alert type="warning" showIcon style={{ marginBottom: 12 }} title="سرویس ارتقا (updater) پاسخ نمی‌دهد"
        description={<>ارتقای بارگذاری‌شده در صف می‌ماند. روی سرور اجرا کنید: <Mono>docker compose up -d updater</Mono> یا از <Mono>scripts/upgrade.sh</Mono> استفاده کنید.</>} />}
      <Upload.Dragger accept=".tar.gz,.tgz,application/gzip" showUploadList={false} disabled={!info.can_verify || progress !== null}
        beforeUpload={(f) => { send(f); return false }}>
        <Typography.Paragraph style={{ margin: 0 }}>بارگذاری بسته‌ی ارتقای امضاشده (<Mono>taktaplus-x.y.z.tar.gz</Mono>)</Typography.Paragraph>
        <Typography.Text type="secondary">پیش از ارتقا امضای بسته با کلید انتشار بررسی می‌شود.</Typography.Text>
      </Upload.Dragger>
      {progress !== null && <Progress percent={progress} status="active" style={{ marginTop: 8 }} />}
      {upgrades.length > 0 && (
        <Collapse style={{ marginTop: 16 }} defaultActiveKey={upgrades.filter((u) => u.status === 'running' || u.status === 'queued').map((u) => u.id)}
          items={upgrades.map((u) => {
            const [t, c] = upgradeTag[u.status] ?? [u.status, 'default']
            return {
              key: u.id,
              label: <Space wrap><Mono>{u.from_version} → {u.version}</Mono><Tag color={c}>{t}</Tag>
                {u.status === 'running' && u.step && <Typography.Text type="secondary">{stepLabel[u.step] ?? u.step}</Typography.Text>}
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>{faDateTime(u.created_at)} · {u.uploaded_by}</Typography.Text></Space>,
              extra: u.status === 'queued' && <Button size="small" danger onClick={async (e) => {
                e.stopPropagation()
                try {
                  await api.del(`/system/upgrades/${u.id}`)
                  onChanged()
                } catch (err) {
                  message.error((err as Error).message)
                }
              }}>لغو</Button>,
              children: (
                <>
                  {u.message && <Typography.Paragraph>{u.message}</Typography.Paragraph>}
                  {u.notes && <Typography.Paragraph type="secondary">یادداشت انتشار: {u.notes}</Typography.Paragraph>}
                  {u.log ? <pre dir="ltr" style={{ margin: 0, background: '#0F1B2D', color: '#D7E0EC', borderRadius: 8, padding: 10, fontSize: 12, maxHeight: 320, overflow: 'auto' }}>{u.log}</pre>
                    : <Typography.Text type="secondary">{u.status === 'queued' ? 'منتظر سرویس ارتقا…' : 'لاگی ثبت نشده است.'}</Typography.Text>}
                </>
              ),
            }
          })} />
      )}
      {reauth.modal}
    </Card>
  )
}

export default function SystemPage() {
  const data = useFetch<Overview>('/system')
  const channels = useFetch<{ items: Channel[] }>('/reports/channels')
  useLiveRefresh(['backup', 'upgrade'], data.refresh, (d) => d.system === true || d.status !== undefined)
  const d = data.data
  const reload = data.reload
  const active = !!d && ((d.runs ?? []).some((r) => r.status === 'running') || (d.upgrades ?? []).some((u) => u.status === 'queued' || u.status === 'running'))
  useEffect(() => {
    if (!active) return
    const t = setInterval(() => void reload(), 4000)
    return () => clearInterval(t)
  }, [active, reload])
  return (
    <>
      <PageHeader title="بکاپ و ارتقای سیستم" subtitle="بکاپ رمزنگاری‌شده‌ی خود TaktaPlus و ارتقا با بسته‌ی آفلاین امضاشده" />
      {data.error && <Alert type="error" showIcon title={data.error} style={{ marginBottom: 12 }} />}
      {d && (
        <Row gutter={[16, 16]}>
          <Col xs={24} xl={14}><BackupCard data={d} channels={channels.data?.items ?? []} onChanged={() => void data.reload()} /></Col>
          <Col xs={24} xl={10}>
            <UpgradeCard info={d.info} upgrades={d.upgrades ?? []} onChanged={() => void data.reload()} />
            <Card title="بازگردانی از فایل" style={{ marginTop: 16 }}>
              <Typography.Paragraph>
                بازگردانی همه‌ی داده‌ها را جایگزین می‌کند و سرویس را متوقف می‌کند، پس از روی سرور انجام می‌شود (روی همین سرور یا سروری که با نسخه‌ی {d.info.version} یا جدیدتر نصب شده):
              </Typography.Paragraph>
              <pre dir="ltr" style={{ background: '#F1F4F9', padding: 10, borderRadius: 6, fontSize: 12, margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>sudo /opt/taktaplus/scripts/restore.sh taktaplus-YYYYMMDD-HHMMSS.tpbak</pre>
              <Typography.Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 0 }}>
                عبارت عبور بکاپ پرسیده می‌شود؛ کلید اصلی هم از بکاپ برمی‌گردد.
              </Typography.Paragraph>
            </Card>
          </Col>
          <Col span={24}><RunsCard runs={d.runs ?? []} /></Col>
        </Row>
      )}
      {d && <Typography.Text type="secondary" style={{ fontSize: 12 }}>{faNum((d.runs ?? []).length)} بکاپ ثبت‌شده</Typography.Text>}
    </>
  )
}
