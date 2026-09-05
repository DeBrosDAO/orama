/**
 * Pub/Sub Chat Example
 *
 * Demonstrates a simple chat application using pub/sub with presence tracking.
 * Multiple clients can join a room, send messages, and see who's online.
 */

import { createClient } from '../src/index';
import type { PresenceMember } from '../src/index';

/**
 * The gateway and credentials come from the environment, so an example can be
 * run against a real namespace without editing it:
 *
 *   GATEWAY_BASE_URL=https://ns-myapp.orama-devnet.network \
 *   ORAMA_API_KEY=ak_... pnpm example
 *
 * The default is a node's index gateway on this machine. This used to be a
 * hardcoded port that no gateway has listened on since the port block moved
 * to 10100, so the examples could not be run as written.
 */
const baseURL = process.env.GATEWAY_BASE_URL ?? 'http://localhost:10104';
const apiKey = process.env.ORAMA_API_KEY ?? 'ak_your_key:default';


interface ChatMessage {
  user: string;
  text: string;
  timestamp: number;
}

async function createChatClient(userName: string, roomName: string) {
  const client = createClient({
    baseURL,
    apiKey,
  });

  console.log(`[${userName}] Joining room: ${roomName}...`);

  // Subscribe to chat room with presence
  const subscription = await client.pubsub.subscribe(roomName, {
    onMessage: (msg) => {
      try {
        const chatMsg: ChatMessage = JSON.parse(msg.data);
        const time = new Date(chatMsg.timestamp).toLocaleTimeString();
        console.log(`[${time}] ${chatMsg.user}: ${chatMsg.text}`);
      } catch {
        console.log(`[${userName}] Received: ${msg.data}`);
      }
    },
    onError: (err) => {
      console.error(`[${userName}] Error:`, err.message);
    },
    onClose: () => {
      console.log(`[${userName}] Disconnected from ${roomName}`);
    },
    presence: {
      enabled: true,
      memberId: userName,
      meta: {
        displayName: userName,
        joinedAt: Date.now(),
      },
      onJoin: (member: PresenceMember) => {
        console.log(`[${userName}] 👋 ${member.memberId} joined the room`);
        if (member.meta) {
          console.log(`[${userName}]    Display name: ${member.meta.displayName}`);
        }
      },
      onLeave: (member: PresenceMember) => {
        console.log(`[${userName}] 👋 ${member.memberId} left the room`);
      },
    },
  });

  console.log(`[${userName}] ✓ Joined ${roomName}`);

  // Send a join message
  await sendMessage(client, roomName, userName, 'Hello everyone!');

  // Helper to send messages
  async function sendMessage(client: any, room: string, user: string, text: string) {
    const chatMsg: ChatMessage = {
      user,
      text,
      timestamp: Date.now(),
    };
    await client.pubsub.publish(room, JSON.stringify(chatMsg));
  }

  // Get current presence
  if (subscription.hasPresence()) {
    const members = await subscription.getPresence();
    console.log(`[${userName}] Current members in room (${members.length}):`);
    members.forEach(m => {
      console.log(`[${userName}]   - ${m.memberId} (joined at ${new Date(m.joinedAt).toLocaleTimeString()})`);
    });
  }

  return {
    client,
    subscription,
    sendMessage: (text: string) => sendMessage(client, roomName, userName, text),
  };
}

async function main() {
  const roomName = 'chat:lobby';

  // Create first user
  const alice = await createChatClient('Alice', roomName);

  // Wait a bit
  await new Promise(resolve => setTimeout(resolve, 500));

  // Create second user
  const bob = await createChatClient('Bob', roomName);

  // Wait a bit
  await new Promise(resolve => setTimeout(resolve, 500));

  // Send some messages
  await alice.sendMessage('Hey Bob! How are you?');
  await new Promise(resolve => setTimeout(resolve, 200));

  await bob.sendMessage('Hi Alice! I\'m doing great, thanks!');
  await new Promise(resolve => setTimeout(resolve, 200));

  await alice.sendMessage('That\'s awesome! Want to grab coffee later?');
  await new Promise(resolve => setTimeout(resolve, 200));

  await bob.sendMessage('Sure! See you at 3pm?');
  await new Promise(resolve => setTimeout(resolve, 200));

  await alice.sendMessage('Perfect! See you then! 👋');

  // Wait to receive all messages
  await new Promise(resolve => setTimeout(resolve, 1000));

  // Get final presence count
  const presence = await alice.client.pubsub.getPresence(roomName);
  console.log(`\nFinal presence count: ${presence.count} members`);

  // Leave room
  console.log('\nClosing connections...');
  alice.subscription.close();
  await new Promise(resolve => setTimeout(resolve, 500));

  bob.subscription.close();
  await new Promise(resolve => setTimeout(resolve, 500));

  console.log('\n--- Chat example completed ---');
}

main().catch(console.error);
