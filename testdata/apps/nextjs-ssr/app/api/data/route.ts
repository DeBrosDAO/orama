import { NextResponse } from 'next/server'

export async function GET() {
  return NextResponse.json({
    users: [
      { id: 1, name: 'Alice', email: 'alice@example.com' },
      { id: 2, name: 'Bob', email: 'bob@example.com' },
      { id: 3, name: 'Charlie', email: 'charlie@example.com' }
    ],
    total: 3,
    timestamp: new Date().toISOString()
  })
}

export async function POST(request: Request) {
  const body = await request.json()

  return NextResponse.json({
    success: true,
    created: {
      id: 4,
      ...body,
      createdAt: new Date().toISOString()
    }
  }, { status: 201 })
}
