import type { Metadata } from 'next'
import './globals.css'

export const metadata: Metadata = {
  title: 'Orama Network Next.js Test',
  description: 'E2E testing for Next.js SSR deployments',
}

export default function RootLayout({
  children,
}: {
  children: React.ReactNode
}) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  )
}
