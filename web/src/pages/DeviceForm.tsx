import { useEffect, useState } from 'react'
import { Alert, Button, Card, Col, Descriptions, Form, Input, InputNumber, Radio, Row, Select, Space, Spin, Typography, App as AntApp } from 'antd'
import { useNavigate, useParams } from 'react-router'
import { api, type Device, type DeviceInput, type Discovery, type Group } from '../api'
import { Mono, PageHeader, kindLabel, licenseTag } from '../ui'
import { useFetch } from '../useFetch'

type FormValues = {
  name: string
  kind: 'fortigate' | 'fortiweb'
  host: string
  https_port: number
  ssh_port: number
  transport_order: string
  group_id?: number | null
  tags?: string[]
  api_token?: string
  api_user?: string
  api_pass?: string
  ssh_user?: string
  ssh_password?: string
  ssh_key?: string
}

const orders = [
  { value: 'api_first', label: 'اول API، سپس SSH' },
  { value: 'ssh_first', label: 'اول SSH، سپس API' },
  { value: 'api_only', label: 'فقط API' },
  { value: 'ssh_only', label: 'فقط SSH' },
]

function toInput(v: FormValues, pins: { tls_pin: string; ssh_host_key: string }, withCreds: boolean): DeviceInput {
  const creds = {
    api_token: v.api_token?.trim() || undefined,
    api_user: v.api_user?.trim() || undefined,
    api_pass: v.api_pass || undefined,
    ssh_user: v.ssh_user?.trim() || undefined,
    ssh_password: v.ssh_password || undefined,
    ssh_key: v.ssh_key?.trim() || undefined,
  }
  const hasAny = Object.values(creds).some(Boolean)
  return {
    name: v.name, kind: v.kind, host: v.host.trim(), https_port: v.https_port, ssh_port: v.ssh_port,
    transport_order: v.transport_order, group_id: v.group_id ?? null, tags: v.tags ?? [],
    credentials: withCreds || hasAny ? creds : undefined, ...pins,
  }
}

export default function DeviceForm() {
  const { id } = useParams()
  const editing = id !== undefined
  const navigate = useNavigate()
  const { message } = AntApp.useApp()
  const [form] = Form.useForm<FormValues>()
  const kind = Form.useWatch('kind', form)
  const groups = useFetch<{ items: Group[] }>('/groups')
  const existing = useFetch<{ device: Device }>(editing ? `/devices/${id}` : null)
  const [discovery, setDiscovery] = useState<Discovery | null>(null)
  const [pins, setPins] = useState({ tls_pin: '', ssh_host_key: '' })
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<'test' | 'save' | null>(null)

  useEffect(() => {
    const d = existing.data?.device
    if (!d) return
    form.setFieldsValue({
      name: d.name, kind: d.kind, host: d.host, https_port: d.https_port, ssh_port: d.ssh_port,
      transport_order: d.transport_order, group_id: d.group_id, tags: d.tags,
    })
    setPins({ tls_pin: d.tls_pin, ssh_host_key: d.ssh_host_key })
  }, [existing.data, form])

  const test = async () => {
    const v = await form.validateFields()
    setBusy('test')
    setError(null)
    setDiscovery(null)
    try {
      // A test always fetches fresh pins so a replaced certificate can be accepted.
      const res = await api.post<Discovery>('/devices/discover' + (editing ? `?id=${id}` : ''), toInput(v, { tls_pin: '', ssh_host_key: '' }, !editing))
      setDiscovery(res)
      setPins({ tls_pin: res.tls_pin, ssh_host_key: res.ssh_host_key })
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(null)
    }
  }

  const save = async () => {
    const v = await form.validateFields()
    setBusy('save')
    setError(null)
    try {
      if (editing) {
        await api.put(`/devices/${id}`, toInput(v, pins, false))
        message.success('تغییرات ذخیره شد')
        navigate(`/devices/${id}`)
      } else {
        const d = await api.post<Device>('/devices', toInput(v, pins, true))
        message.success(d.status === 'online' ? 'دستگاه ثبت و شناسایی شد' : 'دستگاه ثبت شد؛ اتصال برقرار نشد')
        navigate(`/devices/${d.id}`)
      }
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(null)
    }
  }

  if (editing && existing.loading) return <Spin />
  const secretHint = editing ? 'برای حفظ مقدار فعلی خالی بگذارید' : undefined

  return (
    <>
      <PageHeader title={editing ? `ویرایش ${existing.data?.device.name ?? ''}` : 'افزودن دستگاه'}
        subtitle="ابتدا اتصال را آزمایش کنید؛ گواهی TLS و کلید SSH دستگاه در اولین اتصال ثبت (Pin) می‌شود." />
      {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 16 }} />}
      <Form form={form} layout="vertical" requiredMark={false}
        initialValues={{ kind: 'fortigate', https_port: 443, ssh_port: 22, transport_order: 'api_first' }}
        onValuesChange={(ch) => { if ('host' in ch || 'https_port' in ch || 'ssh_port' in ch) setPins({ tls_pin: '', ssh_host_key: '' }) }}>
        <Row gutter={16}>
          <Col xs={24} xl={14}>
            <Card title="مشخصات" style={{ marginBottom: 16 }}>
              <Form.Item name="kind" label="نوع">
                <Radio.Group optionType="button" options={[{ value: 'fortigate', label: 'FortiGate' }, { value: 'fortiweb', label: 'FortiWeb' }]} />
              </Form.Item>
              <Row gutter={12}>
                <Col xs={24} md={12}>
                  <Form.Item name="name" label="نام" rules={[{ required: true, message: 'نام دستگاه' }]}><Input /></Form.Item>
                </Col>
                <Col xs={24} md={12}>
                  <Form.Item name="host" label="آدرس مدیریت (IP یا نام)" rules={[{ required: true, message: 'آدرس دستگاه' }]}><Input dir="ltr" /></Form.Item>
                </Col>
                <Col xs={12} md={6}>
                  <Form.Item name="https_port" label="پورت HTTPS"><InputNumber min={1} max={65535} style={{ width: '100%' }} /></Form.Item>
                </Col>
                <Col xs={12} md={6}>
                  <Form.Item name="ssh_port" label="پورت SSH"><InputNumber min={1} max={65535} style={{ width: '100%' }} /></Form.Item>
                </Col>
                <Col xs={24} md={12}>
                  <Form.Item name="transport_order" label="ترتیب اتصال"><Select options={orders} /></Form.Item>
                </Col>
                <Col xs={24} md={12}>
                  <Form.Item name="group_id" label="گروه">
                    <Select allowClear placeholder="بدون گروه" options={(groups.data?.items ?? []).map((g) => ({ value: g.id, label: g.name }))} />
                  </Form.Item>
                </Col>
                <Col xs={24} md={12}>
                  <Form.Item name="tags" label="برچسب‌ها"><Select mode="tags" tokenSeparators={[',']} /></Form.Item>
                </Col>
              </Row>
            </Card>
            <Card title="دسترسی REST API" style={{ marginBottom: 16 }}>
              {kind === 'fortiweb' ? (
                <Row gutter={12}>
                  <Col xs={24} md={12}><Form.Item name="api_user" label="نام کاربری ادمین"><Input dir="ltr" autoComplete="off" /></Form.Item></Col>
                  <Col xs={24} md={12}><Form.Item name="api_pass" label="رمز" extra={secretHint}><Input.Password dir="ltr" autoComplete="new-password" /></Form.Item></Col>
                </Row>
              ) : (
                <Form.Item name="api_token" label="API Token" extra={secretHint ?? 'از System › Administrators › REST API Admin بسازید؛ پروفایل باید دسترسی خواندن (و برای ریستور، نوشتن) داشته باشد.'}>
                  <Input.Password dir="ltr" autoComplete="new-password" />
                </Form.Item>
              )}
            </Card>
            <Card title="دسترسی SSH" style={{ marginBottom: 16 }}>
              <Row gutter={12}>
                <Col xs={24} md={12}><Form.Item name="ssh_user" label="نام کاربری"><Input dir="ltr" autoComplete="off" /></Form.Item></Col>
                <Col xs={24} md={12}><Form.Item name="ssh_password" label="رمز" extra={secretHint}><Input.Password dir="ltr" autoComplete="new-password" /></Form.Item></Col>
              </Row>
              <Form.Item name="ssh_key" label="کلید خصوصی (اختیاری، PEM)" extra={secretHint}>
                <Input.TextArea dir="ltr" rows={3} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" />
              </Form.Item>
            </Card>
          </Col>
          <Col xs={24} xl={10}>
            <Card title="آزمایش اتصال" style={{ marginBottom: 16 }}>
              <Space orientation="vertical" style={{ width: '100%' }}>
                <Button onClick={() => void test()} loading={busy === 'test'} block>آزمایش اتصال و شناسایی</Button>
                {discovery && (
                  <>
                    <Descriptions column={1} size="small" bordered>
                      <Descriptions.Item label="سریال"><Mono>{discovery.info.serial || '—'}</Mono></Descriptions.Item>
                      <Descriptions.Item label="مدل">{discovery.info.model || kindLabel(kind)}</Descriptions.Item>
                      <Descriptions.Item label="فرم‌ور"><Mono>v{discovery.info.firmware} build{discovery.info.build}</Mono></Descriptions.Item>
                      <Descriptions.Item label="Hostname">{discovery.info.hostname || '—'}</Descriptions.Item>
                      <Descriptions.Item label="HA">{discovery.info.ha_mode}</Descriptions.Item>
                      <Descriptions.Item label="لایسنس">{licenseTag(discovery.license)}</Descriptions.Item>
                      {discovery.ha_peers.map((p) => (
                        <Descriptions.Item key={p.serial} label="عضو HA"><Mono>{p.serial}</Mono> {licenseTag(p.license)}</Descriptions.Item>
                      ))}
                    </Descriptions>
                    {discovery.license !== 'active' && discovery.license !== 'grace' && (
                      <Alert type="warning" showIcon title="این سریال در لایسنس نیست؛ دستگاه ثبت می‌شود اما پایش و بکاپ روی آن انجام نمی‌شود." />
                    )}
                    {discovery.info.fortimanager && <Alert type="info" showIcon title="این دستگاه توسط FortiManager مدیریت می‌شود." />}
                  </>
                )}
                {(pins.tls_pin || pins.ssh_host_key) && (
                  <div>
                    <Typography.Text type="secondary" style={{ fontSize: 12 }}>اثرانگشت‌های ثبت‌شده</Typography.Text>
                    {pins.tls_pin && <div style={{ fontSize: 12, wordBreak: 'break-all' }}>TLS SHA-256: <Mono>{pins.tls_pin}</Mono></div>}
                    {pins.ssh_host_key && <div style={{ fontSize: 12, wordBreak: 'break-all' }}>SSH: <Mono>{pins.ssh_host_key.slice(0, 60)}…</Mono></div>}
                  </div>
                )}
              </Space>
            </Card>
            <Space>
              <Button type="primary" onClick={() => void save()} loading={busy === 'save'}>{editing ? 'ذخیره' : 'ثبت دستگاه'}</Button>
              <Button onClick={() => navigate(-1)}>انصراف</Button>
            </Space>
          </Col>
        </Row>
      </Form>
    </>
  )
}
