import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { Terminal } from "../ui/terminal";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { AnimateIn } from "../ui/animate-in";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "../ui/tabs";

export function CliInstall() {
  return (
    <>
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Install the CLI"
              subtitle="Get started in seconds. Available on macOS, Linux, and from source."
            />

            <Tabs defaultValue="brew">
              <TabsList>
                <TabsTrigger value="brew">macOS (Homebrew)</TabsTrigger>
                <TabsTrigger value="apt">Linux (Debian/Ubuntu)</TabsTrigger>
                <TabsTrigger value="source">From Source</TabsTrigger>
              </TabsList>

              <TabsContent value="brew">
                <Terminal
                  lines={[
                    { prefix: "$", text: "brew install DeBrosOfficial/tap/orama" },
                    { prefix: "\u2192", text: "Downloading orama..." },
                    { prefix: "\u2713", text: "orama installed successfully" },
                    { text: "" },
                    { prefix: "$", text: "orama --version" },
                    { prefix: "\u2192", text: "orama v2.0.0" },
                  ]}
                />
              </TabsContent>

              <TabsContent value="apt">
                <Terminal
                  lines={[
                    { prefix: "$", text: "curl -sL https://github.com/DeBrosOfficial/network/releases/latest/download/orama_linux_amd64.deb -o orama.deb" },
                    { prefix: "$", text: "sudo dpkg -i orama.deb" },
                    { prefix: "\u2192", text: "Selecting previously unselected package orama." },
                    { prefix: "\u2713", text: "orama installed successfully" },
                  ]}
                />
              </TabsContent>

              <TabsContent value="source">
                <Terminal
                  lines={[
                    { prefix: "$", text: "go install github.com/DeBrosOfficial/network/cmd/cli@latest" },
                    { prefix: "\u2192", text: "Downloading modules..." },
                    { prefix: "\u2713", text: "orama installed to $GOPATH/bin" },
                  ]}
                />
              </TabsContent>
            </Tabs>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>
    </>
  );
}
