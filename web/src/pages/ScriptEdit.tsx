import { useEffect, useState } from 'react'
import { Alert, Button, Card, Col, Collapse, Form, Input, Popconfirm, Radio, Row, Select, Space, Spin, Tabs, Tag, Typography, App as AntApp } from 'antd'
import { Link, useNavigate, useParams } from 'react-router'
import { api, type DeviceRow, type Script, type ScriptRun } from '../api'
import { useCan } from '../auth'
import { ChangeForm } from '../changeui'
import { faDateTime } from '../format'
import { PageHeader } from '../ui'
import { useFetch } from '../useFetch'

const mono = { fontFamily: 'ui-monospace, Consolas, monospace', fontSize: 13 }

function Outputs({ runs }: { runs: ScriptRun[] }) {
  if (!runs.length) return <Typography.Text type="secondary">هنوز اجرا نشده است.</Typography.Text>
  return (
    <Collapse items={runs.map((r) => ({
      key: r.id || r.device_id,
      label: <Space>{r.device_name}{r.ok ? <Tag color="green">موفق</Tag> : <Tag color="red">ناموفق</Tag>}<Typography.Text type="secondary" style={{ fontSize: 12 }}>{faDateTime(r.at)} · {r.actor}</Typography.Text></Space>,
      children: (
        <>
          {r.error && <Alert type="error" showIcon title={r.error} style={{ marginBottom: 8 }} />}
          <pre dir="ltr" style={{ margin: 0, background: '#1F1F1F', color: '#EEE', padding: 10, borderRadius: 6, fontSize: 12, maxHeight: 420, overflow: 'auto', whiteSpace: 'pre-wrap' }}>{r.output || r.script}</pre>
        </>
      ),
    }))} />
  )
}

export default function ScriptEdit() {
  const { id } = useParams()
  const isNew = id === 'new'
  const navigate = useNavigate()
  const { message } = AntApp.useApp()
  const canCreate = useCan('changes.create')
  const existing = useFetch<{ items: Script[] }>(isNew ? null : '/scripts')
  const script = existing.data?.items.find((s) => String(s.id) === id)
  const devices = useFetch<{ items: DeviceRow[] }>('/devices')
  const runs = useFetch<{ items: ScriptRun[] }>(isNew ? null : `/scripts/${id}/runs`)
  const [form] = Form.useForm<Script>()
  const body = Form.useWatch('body', form) ?? ''
  const [previewDevice, setPreviewDevice] = useState<number | null>(null)
  const [preview, setPreview] = useState<{ text: string; read_only: boolean } | { error: string } | null>(null)
  const [runDevices, setRunDevices] = useState<number[]>([])
  const [lastRun, setLastRun] = useState<ScriptRun[] | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  useEffect(() => {
    if (script) form.setFieldsValue(script)
  }, [script, form])

  if (!isNew && existing.loading) return <Spin />
  if (!isNew && existing.data && !script) return <Alert type="error" showIcon title="اسکریپت پیدا نشد" />

  const save = async (v: Script) => {
    setBusy('save')
    try {
      const saved = isNew ? await api.post<Script>('/scripts', v) : await api.put<Script>(`/scripts/${id}`, v)
      message.success('ذخیره شد')
      if (isNew) navigate(`/scripts/${saved.id}`, { replace: true })
      else void existing.reload()
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setBusy(null)
    }
  }
  const doPreview = async (deviceId: number) => {
    setPreviewDevice(deviceId)
    try {
      setPreview(await api.post('/scripts/preview', { body, device_id: deviceId }))
    } catch (e) {
      setPreview({ error: (e as Error).message })
    }
  }
  const runReadOnly = async () => {
    setBusy('run')
    try {
      const r = await api.post<{ items: ScriptRun[] }>(`/scripts/${id}/run`, { device_ids: runDevices })
      setLastRun(r.items)
      void runs.reload()
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setBusy(null)
    }
  }
  const dirty = !!script && body !== script.body

  return (
    <>
      <Typography.Text type="secondary"><Link to="/scripts">اسکریپت‌ها</Link> / {isNew ? 'جدید' : script?.name}</Typography.Text>
      <PageHeader title={isNew ? 'اسکریپت جدید' : <Space>{script?.name}{script?.read_only ? <Tag color="green">فقط‌خواندنی</Tag> : <Tag color="orange">تغییر پیکربندی</Tag>}</Space>} />
      <Tabs items={[
        {
          key: 'edit', label: 'ویرایش', children: (
            <Row gutter={16}>
              <Col xs={24} xl={14}>
                <Card>
                  <Form form={form} layout="vertical" requiredMark={false} onFinish={save} initialValues={{ platform: 'fortigate' }} disabled={!canCreate}>
                    <Space.Compact block>
                      <Form.Item name="name" label="نام" rules={[{ required: true }]} style={{ flex: 1 }}><Input /></Form.Item>
                      <Form.Item name="platform" label="پلتفرم"><Radio.Group optionType="button" options={[{ value: 'fortigate', label: 'FortiGate' }, { value: 'fortiweb', label: 'FortiWeb' }]} /></Form.Item>
                    </Space.Compact>
                    <Form.Item name="description" label="توضیح"><Input /></Form.Item>
                    <Form.Item name="body" label="متن اسکریپت" rules={[{ required: true }]}
                      extra="اسکریپتی که فقط دستورهای get، show و diagnose داشته باشد فقط‌خواندنی است و بدون تأیید اجرا می‌شود.">
                      <Input.TextArea dir="ltr" rows={14} style={mono} spellCheck={false}
                        placeholder={'config system dns\n    set primary {{ .Vars.dns_primary }}\nend'} />
                    </Form.Item>
                    <Space>
                      <Button type="primary" htmlType="submit" loading={busy === 'save'}>ذخیره</Button>
                      {!isNew && (
                        <Popconfirm title="اسکریپت حذف شود؟" okText="حذف" cancelText="انصراف"
                          onConfirm={async () => { await api.del(`/scripts/${id}`).catch((e) => message.error(e.message)); navigate('/scripts') }}>
                          <Button danger>حذف</Button>
                        </Popconfirm>
                      )}
                    </Space>
                  </Form>
                </Card>
              </Col>
              <Col xs={24} xl={10}>
                <Card title="پیش‌نمایش برای یک دستگاه">
                  <Select style={{ width: '100%', marginBottom: 12 }} showSearch optionFilterProp="label" placeholder="انتخاب دستگاه" value={previewDevice}
                    onChange={(v) => void doPreview(v)} options={(devices.data?.items ?? []).map((d) => ({ value: d.id, label: d.name }))} />
                  {preview && ('error' in preview
                    ? <Alert type="error" showIcon title={preview.error} />
                    : <pre dir="ltr" style={{ margin: 0, background: '#FAFAFA', border: '1px solid #EEE', padding: 10, borderRadius: 6, fontSize: 12, whiteSpace: 'pre-wrap' }}>{preview.text}</pre>)}
                  {previewDevice && <Button size="small" type="link" onClick={() => void doPreview(previewDevice)}>به‌روزرسانی پیش‌نمایش</Button>}
                </Card>
              </Col>
            </Row>
          ),
        },
        ...(!isNew && script ? [{
          key: 'run', label: 'اجرا', children: dirty ? <Alert type="warning" showIcon title="ابتدا تغییرات را ذخیره کنید." /> : script.read_only ? (
            <Card>
              <Space.Compact block style={{ marginBottom: 16 }}>
                <Select mode="multiple" style={{ flex: 1 }} showSearch optionFilterProp="label" placeholder="دستگاه‌ها" value={runDevices} onChange={setRunDevices}
                  options={(devices.data?.items ?? []).map((d) => ({ value: d.id, label: d.name }))} />
                <Button type="primary" disabled={!runDevices.length || !canCreate} loading={busy === 'run'} onClick={() => void runReadOnly()}>اجرا</Button>
              </Space.Compact>
              {lastRun && <Outputs runs={lastRun} />}
            </Card>
          ) : (
            <ChangeForm kind="script" defaultTitle={script.name} spec={() => ({ script_id: script.id })}>
              <Alert type="info" showIcon style={{ marginBottom: 16 }} title="این اسکریپت پیکربندی را تغییر می‌دهد و از طریق درخواست تغییر و تأیید اجرا می‌شود. متن نهایی هر دستگاه در پیش‌نمایش درخواست دیده می‌شود." />
            </ChangeForm>
          ),
        }, {
          key: 'history', label: 'خروجی‌های قبلی', children: <Card><Outputs runs={runs.data?.items ?? []} /></Card>,
        }] : []),
      ]} />
    </>
  )
}
