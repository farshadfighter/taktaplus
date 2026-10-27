import { useEffect, useState } from 'react'
import { Alert, Button, Card, Col, Form, Input, InputNumber, Radio, Row, Select, Space, Switch, Table, Tabs, Tag, Typography, App as AntApp } from 'antd'
import { api, type OtpEvent, type OtpOverview, type RadiusClient, type SmsMessage } from '../api'
import { useCan } from '../auth'
import { faDateTime, faNum } from '../format'
import { Mono, PageHeader } from '../ui'
import { useFetch } from '../useFetch'

// SettingForm loads a settings object into a form and saves it with PUT.
function SettingForm({ path, value, children, onSaved }: { path: string; value: object | undefined; children: React.ReactNode; onSaved: () => void }) {
  const { message } = AntApp.useApp()
  const [form] = Form.useForm()
  const canManage = useCan('otp.manage')
  useEffect(() => { if (value) form.setFieldsValue(value) }, [value, form])
  return (
    <Form form={form} layout="vertical" requiredMark={false} disabled={!canManage} onFinish={async (v) => {
      try {
        await api.put(path, { ...value, ...v })
        message.success('ذخیره شد')
        onSaved()
      } catch (e) {
        message.error((e as Error).message)
      }
    }}>
      {children}
      <Button type="primary" htmlType="submit">ذخیره</Button>
    </Form>
  )
}

function CLIBox({ query }: { query: string }) {
  const [text, setText] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    setText(null)
    setError(null)
    api.get<{ cli: string }>('/otp/fortigate-config?' + query).then((r) => setText(r.cli)).catch((e) => setError(e.message))
  }, [query])
  if (error) return <Alert type="warning" showIcon title={error} />
  return text ? (
    <Typography.Paragraph copyable={{ text }} style={{ margin: 0 }}>
      <pre dir="ltr" style={{ background: '#FAFAFA', border: '1px solid #EEE', borderRadius: 6, padding: 10, fontSize: 12, margin: 0 }}>{text}</pre>
    </Typography.Paragraph>
  ) : null
}

const statusTag = (s: string) => (s === 'sent' ? <Tag color="green">ارسال شد</Tag> : s === 'blocked' ? <Tag color="orange">مسدود</Tag> : <Tag color="red">ناموفق</Tag>)
const eventTag = (e: string) =>
  ({ accept: <Tag color="green">ورود موفق</Tag>, challenge: <Tag color="blue">ارسال کد</Tag>, wrong_code: <Tag color="orange">کد اشتباه</Tag>, reject: <Tag color="red">رد</Tag> })[e] ?? <Tag>{e}</Tag>

export default function OtpSettings() {
  const { message } = AntApp.useApp()
  const canManage = useCan('otp.manage')
  const { data, reload } = useFetch<OtpOverview>('/otp')
  const clients = useFetch<{ items: RadiusClient[] }>('/otp/radius/clients')
  const log = useFetch<{ messages: SmsMessage[]; events: OtpEvent[] }>('/otp/log')
  const [testMobile, setTestMobile] = useState('')
  const [serverIP, setServerIP] = useState(window.location.hostname)
  const [cliDevice, setCliDevice] = useState<number | null>(null)
  const [cliGroup, setCliGroup] = useState('TaktaPlus-2FA')

  const setClient = async (c: RadiusClient, enabled: boolean, rotate = false) => {
    try {
      await api.put(`/otp/radius/clients/${c.device_id}`, { enabled, source_ip: c.source_ip, rotate })
      void clients.reload()
      if (rotate) message.success('کلید جدید ساخته شد؛ تنظیمات فورتی‌گیت را به‌روز کنید')
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  return (
    <>
      <PageHeader title="تنظیمات دو مرحله‌ای" subtitle="سیاست کد، sms.ir، LDAP/AD، سرور RADIUS و دروازه‌ی ایمیل‌به‌پیامک" />
      <Tabs items={[
        {
          key: 'policy', label: 'سیاست کد', children: (
            <Card>
              <SettingForm path="/otp/policy" value={data?.policy} onSaved={reload}>
                <Row gutter={16}>
                  <Col xs={12} md={8}><Form.Item name="length" label="طول کد"><InputNumber min={4} max={8} /></Form.Item></Col>
                  <Col xs={12} md={8}><Form.Item name="ttl_seconds" label="اعتبار کد (ثانیه)"><InputNumber min={30} max={900} /></Form.Item></Col>
                  <Col xs={12} md={8}><Form.Item name="max_attempts" label="تلاش برای هر کد"><InputNumber min={1} max={10} /></Form.Item></Col>
                  <Col xs={12} md={8}><Form.Item name="max_sends" label="حداکثر ارسال در ۱۰ دقیقه"><InputNumber min={1} max={20} /></Form.Item></Col>
                  <Col xs={12} md={8}><Form.Item name="max_failures" label="خطای مجاز پیش از قفل"><InputNumber min={1} max={50} /></Form.Item></Col>
                  <Col xs={12} md={8}><Form.Item name="lockout_minutes" label="مدت قفل (دقیقه)"><InputNumber min={1} max={1440} /></Form.Item></Col>
                </Row>
              </SettingForm>
            </Card>
          ),
        },
        {
          key: 'sms', label: 'sms.ir', children: (
            <Row gutter={16}>
              <Col xs={24} lg={14}>
                <Card>
                  <Typography.Paragraph type="secondary">ارسال با «ارسال سریع» sms.ir: در پنل sms.ir یک قالب با پارامتر کد (مثلاً CODE) بسازید و شناسه‌ی آن را اینجا وارد کنید.</Typography.Paragraph>
                  <SettingForm path="/otp/sms" value={data?.sms} onSaved={reload}>
                    <Form.Item name="api_key" label="کلید API (x-api-key)"><Input.Password dir="ltr" autoComplete="new-password" /></Form.Item>
                    <Space.Compact block>
                      <Form.Item name="template_id" label="شناسه‌ی قالب" style={{ flex: 1 }}><InputNumber style={{ width: '100%' }} min={1} /></Form.Item>
                      <Form.Item name="param" label="نام پارامتر کد" style={{ flex: 1 }}><Input dir="ltr" /></Form.Item>
                    </Space.Compact>
                    <Form.Item name="low_credit" label="هشدار در اعتبار کمتر از (ریال)"><InputNumber style={{ width: 200 }} min={0} /></Form.Item>
                  </SettingForm>
                </Card>
              </Col>
              <Col xs={24} lg={10}>
                <Card title="آزمایش">
                  <Space orientation="vertical" style={{ width: '100%' }}>
                    <Space.Compact block>
                      <Input dir="ltr" placeholder="0912xxxxxxx" value={testMobile} onChange={(e) => setTestMobile(e.target.value)} />
                      <Button disabled={!canManage || !testMobile} onClick={async () => {
                        try { await api.post('/otp/sms/test', { mobile: testMobile }); message.success('پیامک آزمایشی (کد ۱۲۳۴۵۶) ارسال شد') } catch (e) { message.error((e as Error).message) }
                        void log.reload()
                      }}>ارسال آزمایشی</Button>
                    </Space.Compact>
                    <Button disabled={!canManage} onClick={async () => {
                      try { const r = await api.get<{ credit: number }>('/otp/sms/credit'); message.info(`اعتبار: ${faNum(r.credit)}`) } catch (e) { message.error((e as Error).message) }
                    }}>مشاهده‌ی اعتبار</Button>
                  </Space>
                </Card>
              </Col>
            </Row>
          ),
        },
        {
          key: 'ldap', label: 'LDAP / AD', children: (
            <Card>
              <SettingForm path="/otp/ldap" value={data?.ldap} onSaved={reload}>
                <Row gutter={16}>
                  <Col xs={24} md={16}><Form.Item name="url" label="آدرس" extra="ldaps://dc.example.local:636 یا ldap://…:389"><Input dir="ltr" /></Form.Item></Col>
                  <Col xs={24} md={8}><Form.Item name="start_tls" label="StartTLS (برای ldap://)" valuePropName="checked"><Switch /></Form.Item></Col>
                  <Col xs={24} md={12}><Form.Item name="bind_dn" label="حساب سرویس (Bind DN)"><Input dir="ltr" /></Form.Item></Col>
                  <Col xs={24} md={12}><Form.Item name="bind_password" label="رمز حساب سرویس"><Input.Password dir="ltr" autoComplete="new-password" /></Form.Item></Col>
                  <Col xs={24}><Form.Item name="base_dn" label="Base DN"><Input dir="ltr" placeholder="DC=example,DC=local" /></Form.Item></Col>
                  <Col xs={24} md={12}><Form.Item name="user_filter" label="فیلتر یافتن کاربر (%s = نام کاربری)"><Input dir="ltr" /></Form.Item></Col>
                  <Col xs={24} md={12}><Form.Item name="import_filter" label="فیلتر کاربران قابل ورود"><Input dir="ltr" /></Form.Item></Col>
                  <Col xs={8}><Form.Item name="user_attr" label="ویژگی نام کاربری"><Input dir="ltr" /></Form.Item></Col>
                  <Col xs={8}><Form.Item name="mobile_attr" label="ویژگی موبایل"><Input dir="ltr" /></Form.Item></Col>
                  <Col xs={8}><Form.Item name="name_attr" label="ویژگی نام"><Input dir="ltr" /></Form.Item></Col>
                </Row>
              </SettingForm>
            </Card>
          ),
        },
        {
          key: 'radius', label: 'RADIUS', children: (
            <Space orientation="vertical" style={{ width: '100%' }} size={16}>
              <Card title={<span>سرور RADIUS {data?.listen.radius ? <Tag color="green">روی {data.listen.radius}</Tag> : <Tag>غیرفعال</Tag>}</span>}>
                <SettingForm path="/otp/radius" value={data?.radius} onSaved={reload}>
                  <Form.Item name="message" label="پیام درخواست کد (نمایش در فورتی‌گیت)"><Input /></Form.Item>
                  <Form.Item name="require_message_authenticator" label="الزام Message-Authenticator" valuePropName="checked"
                    extra="درخواست‌های بدون امضای معتبر نادیده گرفته می‌شوند (محافظت در برابر جعل و BlastRADIUS). FortiOS به‌روز آن را می‌فرستد.">
                    <Switch />
                  </Form.Item>
                </SettingForm>
              </Card>
              <Card title="فورتی‌گیت‌های مجاز (Client)">
                <Table<RadiusClient> rowKey="device_id" size="small" pagination={false} dataSource={clients.data?.items ?? []}
                  columns={[
                    { title: 'دستگاه', dataIndex: 'device_name' },
                    { title: 'آدرس', render: (_, c) => <Mono>{c.host}</Mono> },
                    { title: 'وضعیت', render: (_, c) => <Switch disabled={!canManage} checked={c.enabled} onChange={(v) => void setClient(c, v)} /> },
                    { title: '', render: (_, c) => c.has_secret && canManage && <Button size="small" type="link" onClick={() => void setClient(c, c.enabled, true)}>کلید جدید</Button> },
                  ]} />
                <Typography.Title level={5} style={{ marginTop: 16 }}>تنظیمات لازم روی فورتی‌گیت</Typography.Title>
                <Space wrap style={{ marginBottom: 12 }}>
                  <Select placeholder="دستگاه" style={{ width: 200 }} value={cliDevice} onChange={setCliDevice}
                    options={(clients.data?.items ?? []).filter((c) => c.has_secret).map((c) => ({ value: c.device_id, label: c.device_name }))} />
                  <Input addonBefore="آدرس TaktaPlus" dir="ltr" value={serverIP} onChange={(e) => setServerIP(e.target.value)} style={{ width: 280 }} />
                  <Input addonBefore="گروه" dir="ltr" value={cliGroup} onChange={(e) => setCliGroup(e.target.value)} style={{ width: 240 }} />
                </Space>
                {cliDevice && <CLIBox query={`device=${cliDevice}&server=${encodeURIComponent(serverIP)}&group=${encodeURIComponent(cliGroup)}`} />}
                <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginTop: 8 }}>
                  این دستورها را با یک اسکریپت CLI و درخواست تغییر روی دستگاه اعمال کنید. گروه را در Policy مربوط به VPN استفاده کنید.
                </Typography.Paragraph>
              </Card>
            </Space>
          ),
        },
        {
          key: 'gateway', label: 'دروازه‌ی ایمیل‌به‌پیامک', children: (
            <Space orientation="vertical" style={{ width: '100%' }} size={16}>
              <Card title={<span>دروازه {data?.listen.smtp ? <Tag color="green">روی {data.listen.smtp}</Tag> : <Tag>غیرفعال</Tag>}</span>}>
                <Typography.Paragraph type="secondary">فورتی‌گیت کد کاربران Local را به آدرس «شماره@دامنه» می‌فرستد؛ سایر ایمیل‌های فورتی‌گیت به سرور ایمیل سازمان فرستاده می‌شوند.</Typography.Paragraph>
                <SettingForm path="/otp/gateway" value={data?.gateway} onSaved={reload}>
                  <Form.Item name="domain" label="دامنه‌ی پیامک"><Input dir="ltr" /></Form.Item>
                  <Row gutter={16}>
                    <Col xs={24} md={12}><Form.Item name="relay_host" label="سرور ایمیل سازمان (Relay)"><Input dir="ltr" /></Form.Item></Col>
                    <Col xs={12} md={4}><Form.Item name="relay_port" label="پورت"><InputNumber min={1} max={65535} /></Form.Item></Col>
                    <Col xs={12} md={8}><Form.Item name="relay_security" label="امنیت"><Radio.Group options={[{ value: 'starttls', label: 'STARTTLS' }, { value: 'tls', label: 'TLS' }, { value: 'none', label: 'هیچ' }]} /></Form.Item></Col>
                    <Col xs={24} md={12}><Form.Item name="relay_user" label="نام کاربری"><Input dir="ltr" autoComplete="off" /></Form.Item></Col>
                    <Col xs={24} md={12}><Form.Item name="relay_password" label="رمز"><Input.Password dir="ltr" autoComplete="new-password" /></Form.Item></Col>
                  </Row>
                </SettingForm>
              </Card>
              <Card title="تنظیمات لازم روی فورتی‌گیت">
                <Input addonBefore="آدرس TaktaPlus" dir="ltr" value={serverIP} onChange={(e) => setServerIP(e.target.value)} style={{ width: 280, marginBottom: 12 }} />
                <CLIBox query={`mode=gateway&server=${encodeURIComponent(serverIP)}`} />
              </Card>
            </Space>
          ),
        },
        {
          key: 'log', label: 'گزارش‌ها', children: (
            <Row gutter={16}>
              <Col xs={24} xl={12}>
                <Card title="ورودها">
                  <Table<OtpEvent> rowKey="id" size="small" dataSource={log.data?.events ?? []} pagination={{ pageSize: 15, hideOnSinglePage: true }} scroll={{ x: 500 }}
                    columns={[
                      { title: 'زمان', render: (_, e) => faDateTime(e.at) },
                      { title: 'کاربر', render: (_, e) => <Mono>{e.username}</Mono> },
                      { title: 'رویداد', render: (_, e) => eventTag(e.event) },
                      { title: 'جزئیات', dataIndex: 'detail', ellipsis: true },
                    ]} />
                </Card>
              </Col>
              <Col xs={24} xl={12}>
                <Card title="پیامک‌ها">
                  <Table<SmsMessage> rowKey="id" size="small" dataSource={log.data?.messages ?? []} pagination={{ pageSize: 15, hideOnSinglePage: true }} scroll={{ x: 500 }}
                    columns={[
                      { title: 'زمان', render: (_, m) => faDateTime(m.at) },
                      { title: 'موبایل', render: (_, m) => <Mono>{m.mobile}</Mono> },
                      { title: 'مسیر', render: (_, m) => ({ radius: 'RADIUS', gateway: 'دروازه', test: 'آزمایش' })[m.purpose] ?? m.purpose },
                      { title: 'وضعیت', render: (_, m) => <span>{statusTag(m.status)}{m.error && <div style={{ fontSize: 12, color: '#CF1322' }}>{m.error}</div>}</span> },
                    ]} />
                </Card>
              </Col>
            </Row>
          ),
        },
      ]} />
    </>
  )
}
