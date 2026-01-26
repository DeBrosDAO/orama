const express = require('express');
const app = express();

const PORT = process.env.PORT || 3000;
const DATABASE_NAME = process.env.DATABASE_NAME || '';
const GATEWAY_URL = process.env.GATEWAY_URL || 'http://localhost:6001';
const API_KEY = process.env.API_KEY || '';

// In-memory storage for simple tests
let items = [
  { id: 1, name: 'Item 1', description: 'First item' },
  { id: 2, name: 'Item 2', description: 'Second item' }
];
let nextId = 3;

app.use(express.json());

// Health check
app.get('/health', (req, res) => {
  res.json({
    status: 'healthy',
    timestamp: new Date().toISOString(),
    service: 'nodejs-api-test',
    config: {
      port: PORT,
      databaseName: DATABASE_NAME ? '[configured]' : '[not configured]',
      gatewayUrl: GATEWAY_URL
    }
  });
});

// Root endpoint
app.get('/', (req, res) => {
  res.json({
    message: 'Orama Network Node.js API Test',
    version: '1.0.0',
    endpoints: {
      health: 'GET /health',
      items: 'GET/POST /api/items',
      item: 'GET/PUT/DELETE /api/items/:id'
    }
  });
});

// List items
app.get('/api/items', (req, res) => {
  res.json({
    items: items,
    total: items.length
  });
});

// Get single item
app.get('/api/items/:id', (req, res) => {
  const id = parseInt(req.params.id);
  const item = items.find(i => i.id === id);

  if (!item) {
    return res.status(404).json({ error: 'Item not found' });
  }

  res.json(item);
});

// Create item
app.post('/api/items', (req, res) => {
  const { name, description } = req.body;

  if (!name) {
    return res.status(400).json({ error: 'Name is required' });
  }

  const newItem = {
    id: nextId++,
    name: name,
    description: description || ''
  };

  items.push(newItem);

  res.status(201).json({
    success: true,
    item: newItem
  });
});

// Update item
app.put('/api/items/:id', (req, res) => {
  const id = parseInt(req.params.id);
  const index = items.findIndex(i => i.id === id);

  if (index === -1) {
    return res.status(404).json({ error: 'Item not found' });
  }

  const { name, description } = req.body;

  if (name) items[index].name = name;
  if (description !== undefined) items[index].description = description;

  res.json({
    success: true,
    item: items[index]
  });
});

// Delete item
app.delete('/api/items/:id', (req, res) => {
  const id = parseInt(req.params.id);
  const index = items.findIndex(i => i.id === id);

  if (index === -1) {
    return res.status(404).json({ error: 'Item not found' });
  }

  items.splice(index, 1);

  res.json({
    success: true,
    message: 'Item deleted'
  });
});

// Echo endpoint (useful for testing)
app.post('/api/echo', (req, res) => {
  res.json({
    received: req.body,
    timestamp: new Date().toISOString()
  });
});

app.listen(PORT, () => {
  console.log(`Node.js API listening on port ${PORT}`);
  console.log(`Database: ${DATABASE_NAME || 'not configured'}`);
  console.log(`Gateway: ${GATEWAY_URL}`);
});
