import { useState } from 'react'
import { Alert, Button, Card, Col, Descriptions, Input, Row, Space, Spin, Table, Tag, Typography, Upload, App as AntApp } from 'antd'
import { api, type LicenseInfo, type LicenseItem } from '../api'
import { useCan } from '../auth'
import { saveText } from '../download'
import { faDate, faDateTime, faNum } from '../format'
import { Mono, PageHeader, kindLabel, licenseTag } from '../ui'
import { useFetch } from '../useFetch'

export default function License() {
  const { message } = AntApp.useApp()
  const canManage = useCan('license.manage')
  const { data, loading, setData } = useFetch<LicenseInfo>('/license')
  const [key, setKey] = useState('')
  const [busy, setBusy] = useState<string | null>(null)

  const run = async (name: string, fn: () => Promise<void>) => {
    setBusy(name)
    try {
      await fn()
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setBusy(null)
    }
  }
  if (loading && !data) return <Spin />
  if (!data) return null
  const lic = data.license

  const activate = () => run('activate', async () => {
    setData(await api.post<LicenseInfo>('/license/activate', { key: key.trim() }))
    message.success('لایسنس فعال شد')
    setKey('')
  })
  const request = () => run('request', async () => {
    const text = await api.post<string>('/license/request', { key: key.trim() })
    saveText(`taktaplus-${new Date().toISOString().slice(0, 10)}.tpreq`, text)
  })
  const upload = (file: File) => {
    void run('upload', async () => {
      setData(await api.post<LicenseInfo>('/license/upload', { license: await file.text() }))
      message.success('فایل لایسنس نصب شد')
    })
    return false
  }
  const heartbeat = () => run('heartbeat', async () => {
    setData(await api.post<LicenseInfo>('/license/heartbeat'))
    message.success('با سرور لایسنس همگام شد')
  })

  return (
    <>
      <PageHeader title="لایسنس" subtitle={<span>شناسه‌ی نصب: <Typography.Text copyable={{ text: data.installation_id }}><Mono>{data.installation_id}</Mono></Typography.Text></span>} />
      {data.clock_rolled_back && <Alert type="error" showIcon style={{ marginBottom: 16 }} title="ساعت سیستم از آخرین زمان ثبت‌شده عقب‌تر است؛ زمان ثبت‌شده مبنای بررسی لایسنس است." />}
      <Row gutter={[16, 16]}>
        <Col xs={24} xl={14}>
          <Card title="لایسنس نصب‌شده" style={{ marginBottom: 16 }}>
            {!lic ? <Alert type="warning" showIcon title="لایسنسی نصب نشده است." /> : (
              <>
                <Descriptions size="small" column={{ xs: 1, md: 2 }}>
                  <Descriptions.Item label="شماره">{lic.license_id}</Descriptions.Item>
                  <Descriptions.Item label="مشتری">{lic.customer.name}</Descriptions.Item>
                  <Descriptions.Item label="صدور">{faDateTime(lic.issued_at)}</Descriptions.Item>
                  <Descriptions.Item label="اعتبار فایل تا">{faDate(lic.not_after)}</Descriptions.Item>
                  <Descriptions.Item label="مهلت پس از انقضا">{faNum(lic.grace_days)} روز</Descriptions.Item>
                  <Descriptions.Item label="حالت">{lic.online ? 'آنلاین (Heartbeat)' : 'آفلاین'}</Descriptions.Item>
                  <Descriptions.Item label="ظرفیت کاربر 2FA">{faNum(lic.otp_capacity)}</Descriptions.Item>
                </Descriptions>
                <Table<LicenseItem> rowKey={(i, n) => `${i.kind}-${i.serial ?? n}`} size="small" pagination={false} dataSource={lic.items} style={{ marginTop: 12 }}
                  columns={[
                    { title: 'قلم', render: (_, i) => (i.kind === 'device' ? <span><Mono>{i.serial}</Mono> <Typography.Text type="secondary">{kindLabel(i.platform ?? '')}</Typography.Text></span> : `${faNum(i.users)} کاربر 2FA`) },
                    { title: 'انقضا', render: (_, i) => faDate(i.expires) },
                    { title: 'وضعیت', render: (_, i) => licenseTag(i.status) },
                  ]} />
              </>
            )}
          </Card>
          {lic?.online && (
            <Card title="ارتباط با سرور لایسنس">
              <Descriptions size="small" column={1}>
                <Descriptions.Item label="آخرین Heartbeat">{faDateTime(data.state.last_heartbeat_at)} {data.state.last_heartbeat_status && <Tag>{data.state.last_heartbeat_status}</Tag>}</Descriptions.Item>
                {data.state.last_error && <Descriptions.Item label="آخرین خطا"><Typography.Text type="danger">{data.state.last_error}</Typography.Text></Descriptions.Item>}
              </Descriptions>
              {canManage && <Button onClick={() => void heartbeat()} loading={busy === 'heartbeat'}>همگام‌سازی اکنون</Button>}
            </Card>
          )}
        </Col>
        {canManage && (
          <Col xs={24} xl={10}>
            <Card title="فعال‌سازی یا تمدید">
              <Space orientation="vertical" style={{ width: '100%' }}>
                <Input dir="ltr" placeholder="TPK-XXXX-XXXX-XXXX-XXXX" value={key} onChange={(e) => setKey(e.target.value)} />
                {data.online_configured ? (
                  <Button type="primary" block disabled={!key.trim()} onClick={() => void activate()} loading={busy === 'activate'}>فعال‌سازی آنلاین</Button>
                ) : (
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>آدرس سرور لایسنس تنظیم نشده؛ از فعال‌سازی آفلاین استفاده کنید.</Typography.Text>
                )}
                <Typography.Title level={5} style={{ marginTop: 12 }}>فعال‌سازی آفلاین</Typography.Title>
                <Typography.Paragraph type="secondary" style={{ fontSize: 13 }}>
                  ۱. فایل درخواست را بگیرید (بدون کلید، درخواست تمدید لایسنس فعلی ساخته می‌شود). ۲. آن را در پورتال لایسنس یا برای پشتیبانی ارسال کنید. ۳. فایل ‎.tplic دریافتی را اینجا بارگذاری کنید.
                </Typography.Paragraph>
                <Button block onClick={() => void request()} loading={busy === 'request'} disabled={!key.trim() && !lic}>دریافت فایل درخواست (‎.tpreq)</Button>
                <Upload accept=".tplic,.txt" showUploadList={false} beforeUpload={upload}>
                  <Button block loading={busy === 'upload'}>بارگذاری فایل لایسنس (‎.tplic)</Button>
                </Upload>
              </Space>
            </Card>
          </Col>
        )}
      </Row>
    </>
  )
}
