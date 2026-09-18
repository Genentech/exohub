# ExoHub Website

Hugo-based documentation website for ExoHub.

## Prerequisites

- Hugo (extended version recommended)
- Go (for theme management)

## Quick Start

1. Install dependencies:
   ```bash
   hugo mod get
   ```

2. Run development server:
   ```bash
   hugo server -D
   ```

3. Build for production:
   ```bash
   hugo
   ```

## Project Structure

- `content/` - Markdown content files
- `config/` - Hugo configuration files
- `layouts/` - Custom layout templates
- `static/` - Static assets (images, CSS, JS)
- `themes/` - Hugo themes
- `assets/` - Asset files to be processed

## Theme

This site uses the Blowfish theme. To enable it:

1. Uncomment the theme line in `config/_default/hugo.toml`
2. Add the theme module in `config/_default/module.toml`

Or install it as a git submodule in the `themes/` directory.

## Configuration

Main configuration files are located in `config/_default/`:

- `hugo.toml` - General site configuration
- `params.toml` - Theme parameters
- `menus.en.toml` - Site navigation menus
- `languages.en.toml` - Language settings
- `markup.toml` - Markdown rendering settings
- `module.toml` - Hugo module configuration

## Creating Content

Create new content pages:

```bash
hugo new content/section-name/page-name.md
```

## Deployment

Build the site for production:

```bash
hugo --minify
```

The built site will be in the `public/` directory.
