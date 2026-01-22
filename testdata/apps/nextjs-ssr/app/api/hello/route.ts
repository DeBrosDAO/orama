import { NextResponse } from 'next/server'

export async function GET() {
  return NextResponse.json({
    message: 'Hello from Orama Network!',
    timestamp: new Date().toISOString(),
    service: 'nextjs-ssr-test'
  })
}
