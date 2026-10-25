import { useEffect, useState } from 'react'
import { Card, Descriptions, Layout, Typography } from 'antd'

type Version = { version: string; commit: string; date: string }

export default function App() {
  const [version, setVersion] = useState<Version | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetch('/api/v1/version')
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(r.statusText))))
      .then(setVersion)
      .catch((e: Error) => setError(e.message))
  }, [])

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Layout.Header>
        <Typography.Title level={4} style={{ color: '#fff', margin: '16px 0' }}>
          تکتاپلاس
        </Typography.Title>
      </Layout.Header>
      <Layout.Content style={{ padding: 24 }}>
        <Card title="وضعیت سرور">
          {error ? (
            <Typography.Text type="danger">خطا در ارتباط با سرور: {error}</Typography.Text>
          ) : (
            <Descriptions column={1}>
              <Descriptions.Item label="نسخه">{version?.version ?? '…'}</Descriptions.Item>
              <Descriptions.Item label="کامیت">{version?.commit ?? '…'}</Descriptions.Item>
            </Descriptions>
          )}
        </Card>
      </Layout.Content>
    </Layout>
  )
}
