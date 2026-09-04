import { describe, expect, it, vi } from 'vitest';
import { PubSubClient, Subscription } from '../../../src/pubsub/client';
import { HttpClient } from '../../../src/core/http';
import { WSClient } from '../../../src/core/ws';
import type { PubSubMessage } from '../../../src/pubsub/types';

/**
 * `publish` accepts a `Uint8Array` and the README calls the SDK "binary-safe",
 * but the receive path decoded straight to UTF-8 text. Anything that was not
 * valid UTF-8 came back with its bytes replaced by U+FFFD, so a published
 * binary payload could not be read back at all.
 */

/** A WSClient stand-in that lets a test push an envelope at the subscription. */
function fakeSocket() {
  let handler: ((data: string) => void) | undefined;
  const ws = {
    onMessage: (fn: (data: string) => void) => {
      handler = fn;
      return () => undefined;
    },
    onError: () => () => undefined,
    onClose: () => () => undefined,
    close: () => undefined,
    isConnected: () => true,
  } as unknown as WSClient;

  return {
    ws,
    deliver: (envelope: unknown) => handler?.(JSON.stringify(envelope)),
  };
}

function subscribe(topic = 'room') {
  const socket = fakeSocket();
  const received: PubSubMessage[] = [];
  const errors: Error[] = [];
  const subscription = new Subscription(socket.ws, topic, undefined, async () => ({
    topic,
    members: [],
    count: 0,
  }));
  subscription.onMessage((message) => received.push(message));
  subscription.onError((error) => errors.push(error));
  return { ...socket, received, errors };
}

function base64(bytes: Uint8Array): string {
  return Buffer.from(bytes).toString('base64');
}

describe('a subscription delivers the bytes that were published', () => {
  it('gives text messages a usable string', () => {
    const { deliver, received } = subscribe();

    deliver({ topic: 'room', data: base64(new TextEncoder().encode('hello')), timestamp: 1 });

    expect(received).toHaveLength(1);
    expect(received[0].data).toBe('hello');
  });

  it('gives every message the exact bytes as well', () => {
    const { deliver, received } = subscribe();

    deliver({ topic: 'room', data: base64(new TextEncoder().encode('hello')), timestamp: 1 });

    expect(Array.from(received[0].bytes)).toEqual([104, 101, 108, 108, 111]);
  });

  /**
   * The bytes that broke the old path: 0xFF and 0xFE are not valid UTF-8, so a
   * text decode replaces them and the payload cannot be recovered.
   */
  it('round-trips a payload that is not text', () => {
    const { deliver, received } = subscribe();
    const payload = new Uint8Array([0x00, 0xff, 0xfe, 0x01, 0x80, 0x7f]);

    deliver({ topic: 'room', data: base64(payload), timestamp: 2 });

    expect(Array.from(received[0].bytes)).toEqual(Array.from(payload));
    // The text view of the same message is lossy, which is why `bytes` exists.
    expect(received[0].data).not.toBe(String.fromCharCode(...payload));
  });

  it('carries the topic and timestamp through', () => {
    const { deliver, received } = subscribe('orders');

    deliver({ topic: 'orders', data: base64(new Uint8Array([1])), timestamp: 1700000000 });

    expect(received[0].topic).toBe('orders');
    expect(received[0].timestamp).toBe(1700000000);
  });

  it('reports a malformed envelope to the error handler rather than delivering it', () => {
    const { deliver, received, errors } = subscribe();

    deliver({ topic: 'room', timestamp: 1 });

    expect(received).toHaveLength(0);
    expect(errors).toHaveLength(1);
  });
});

describe('publishing', () => {
  function publisher() {
    const sent: any[] = [];
    const fetchImpl = vi.fn(async (_url: any, init: any) => {
      sent.push(JSON.parse(init.body));
      return new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    });
    const http = new HttpClient({ baseURL: 'https://gw.example', fetch: fetchImpl as any });
    return { pubsub: new PubSubClient(http, { wsURL: 'wss://gw.example' }), sent };
  }

  it('sends a string payload as base64', async () => {
    const { pubsub, sent } = publisher();
    await pubsub.publish('room', 'hello');
    expect(sent[0].data_base64).toBe(base64(new TextEncoder().encode('hello')));
  });

  it('sends a binary payload as the same bytes', async () => {
    const { pubsub, sent } = publisher();
    const payload = new Uint8Array([0x00, 0xff, 0xfe]);
    await pubsub.publish('room', payload);
    expect(sent[0].data_base64).toBe(base64(payload));
  });

  it('round-trips binary through publish and receive', async () => {
    const { pubsub, sent } = publisher();
    const payload = new Uint8Array([0xde, 0xad, 0xbe, 0xef]);
    await pubsub.publish('room', payload);

    const { deliver, received } = subscribe();
    deliver({ topic: 'room', data: sent[0].data_base64, timestamp: 1 });

    expect(Array.from(received[0].bytes)).toEqual(Array.from(payload));
  });
});
