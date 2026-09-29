// Rendered on the server for every request: the render id and time differ
// between two requests, which a statically exported page cannot do.
const RELEASE = 'RELEASE_MARKER';

export async function getServerSideProps() {
  return {
    props: {
      release: RELEASE,
      renderId: Math.random().toString(36).slice(2),
      renderedAt: new Date().toISOString(),
      region: process.env.ORAMA_NAMESPACE || '',
    },
  };
}

export default function Home({ release, renderId, renderedAt, region }) {
  return (
    <main>
      <h1>Orama reference Next.js app</h1>
      <p id="release">{release}</p>
      <p id="render">{renderId}</p>
      <p id="rendered-at">{renderedAt}</p>
      <p id="namespace">{region}</p>
    </main>
  );
}
