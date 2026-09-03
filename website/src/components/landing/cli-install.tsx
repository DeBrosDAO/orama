import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { Terminal } from "../ui/terminal";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { AnimateIn } from "../ui/animate-in";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "../ui/tabs";
import { GITHUB_URL } from "../../data/navigation";

export function CliInstall() {
  return (
    <>
      <Section id="install">
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Install the CLI"
              subtitle="One Go binary. Homebrew, a Debian package, or built from source."
            />

            <Tabs defaultValue="brew">
              <TabsList>
                <TabsTrigger value="brew">Homebrew</TabsTrigger>
                <TabsTrigger value="deb">Debian / Ubuntu</TabsTrigger>
                <TabsTrigger value="source">From source</TabsTrigger>
              </TabsList>

              <TabsContent value="brew">
                <Terminal
                  lines={[
                    { prefix: "$", text: "brew install DeBrosDAO/tap/orama" },
                    { prefix: "✓", text: "orama installed" },
                    { text: "" },
                    { prefix: "$", text: "orama version" },
                  ]}
                />
              </TabsContent>

              <TabsContent value="deb">
                <Terminal
                  lines={[
                    {
                      prefix: "#",
                      text: "grab the .deb for your arch from the releases page",
                    },
                    { prefix: "$", text: "sudo dpkg -i orama_*_linux_amd64.deb" },
                    { prefix: "✓", text: "orama installed to /usr/bin/orama" },
                  ]}
                />
              </TabsContent>

              <TabsContent value="source">
                <Terminal
                  lines={[
                    {
                      prefix: "$",
                      text: "git clone https://github.com/DeBrosDAO/orama.git",
                    },
                    { prefix: "$", text: "cd orama && make build" },
                    { prefix: "✓", text: "binaries written to core/bin/" },
                    { text: "" },
                    { prefix: "$", text: "./core/bin/orama version" },
                  ]}
                />
              </TabsContent>
            </Tabs>

            <p className="text-sm text-muted">
              Homebrew tracks the latest stable release. Nightly builds and the
              Debian packages are published on the{" "}
              <a
                href={`${GITHUB_URL}/releases`}
                target="_blank"
                rel="noopener noreferrer"
                className="text-accent hover:underline underline-offset-4"
              >
                releases page
              </a>
              .
            </p>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>
    </>
  );
}
