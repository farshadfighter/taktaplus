import { useState } from 'react'
import { Alert, Button, Card, Form, Input, Typography } from 'antd'
import { QRCodeSVG } from 'qrcode.react'
import { useNavigate } from 'react-router'
import { api } from '../api'
import { useAuth } from '../auth'

type Step = 'password' | 'mfa' | 'mfa_setup'

export default function Login() {
  const [step, setStep] = useState<Step>('password')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [setup, setSetup] = useState<{ uri: string; secret: string } | null>(null)
  const { refresh } = useAuth()
  const navigate = useNavigate()

  async function run(fn: () => Promise<void>) {
    setBusy(true)
    setError(null)
    try {
      await fn()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const finish = async () => {
    await refresh()
    navigate('/')
  }

  const onPassword = (v: { username: string; password: string }) =>
    run(async () => {
      const res = await api.post<{ next: Step | 'done' }>('/auth/login', v)
      if (res.next === 'done') return finish()
      if (res.next === 'mfa_setup') setSetup(await api.post<{ uri: string; secret: string }>('/auth/mfa/setup'))
      setStep(res.next)
    })

  const onCode = (v: { code: string }) =>
    run(async () => {
      await api.post(step === 'mfa_setup' ? '/auth/mfa/enable' : '/auth/mfa', { code: v.code.trim() })
      await finish()
    })

  return (
    <div style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 24, background: '#F5F5F6' }}>
      <div style={{ width: '100%', maxWidth: 420, display: 'flex', flexDirection: 'column', gap: 20, alignItems: 'center' }}>
        <img src="/logo.png" alt="TaktaPlus" style={{ width: 220, height: 'auto' }} />
        <Card style={{ width: '100%' }}>
          <Typography.Title level={4} style={{ marginTop: 0 }}>
            {step === 'password' ? 'ورود به تکتاپلاس' : step === 'mfa' ? 'تأیید دو مرحله‌ای' : 'فعال‌سازی تأیید دو مرحله‌ای'}
          </Typography.Title>
          {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 16 }} />}

          {step === 'password' && (
            <Form layout="vertical" onFinish={onPassword} requiredMark={false}>
              <Form.Item name="username" label="نام کاربری" rules={[{ required: true, message: 'نام کاربری را وارد کنید' }]}>
                <Input dir="ltr" autoComplete="username" autoFocus />
              </Form.Item>
              <Form.Item name="password" label="رمز عبور" rules={[{ required: true, message: 'رمز را وارد کنید' }]}>
                <Input.Password dir="ltr" autoComplete="current-password" />
              </Form.Item>
              <Button type="primary" htmlType="submit" block loading={busy}>ادامه</Button>
            </Form>
          )}

          {step !== 'password' && (
            <Form layout="vertical" onFinish={onCode} requiredMark={false}>
              {step === 'mfa_setup' && setup && (
                <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8, marginBottom: 16 }}>
                  <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
                    این کد را با اپلیکیشن Authenticator (مثل Google Authenticator) اسکن کنید.
                  </Typography.Paragraph>
                  <QRCodeSVG value={setup.uri} size={180} />
                  <Typography.Text copyable code dir="ltr">{setup.secret}</Typography.Text>
                </div>
              )}
              <Form.Item name="code" label="کد ۶ رقمی" rules={[{ required: true, pattern: /^\s*\d{6}\s*$/, message: 'کد ۶ رقمی را وارد کنید' }]}>
                <Input dir="ltr" inputMode="numeric" autoComplete="one-time-code" autoFocus style={{ letterSpacing: 8, textAlign: 'center', fontSize: 20 }} />
              </Form.Item>
              <Button type="primary" htmlType="submit" block loading={busy}>ورود</Button>
              <Button type="link" block onClick={() => setStep('password')}>بازگشت</Button>
            </Form>
          )}
        </Card>
      </div>
    </div>
  )
}
