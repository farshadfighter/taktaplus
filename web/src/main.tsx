import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { ConfigProvider } from 'antd'
import faIR from 'antd/locale/fa_IR'
import App from './App'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ConfigProvider direction="rtl" locale={faIR}>
      <App />
    </ConfigProvider>
  </StrictMode>,
)
