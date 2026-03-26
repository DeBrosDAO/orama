package steps

// Welcome step
type Welcome struct{}

// View renders the welcome step
func (w *Welcome) View() string {
	title := titleStyle.Render("Welcome to Orama Network!")
	content := "This wizard will guide you through setting up your node.\n\n" +
		"You'll need:\n" +
		"  • A public IP address for your server\n" +
		"  • A domain name (e.g., node-1.orama.network)\n" +
		"  • For joining: cluster secret from existing node\n"

	return boxStyle.Render(title+"\n\n"+content) + "\n\n" +
		helpStyle.Render("Press Enter to continue • q to quit")
}
