class V2Builder {
    static container(components = []) {
        return {
            type: 17, // Container
            components: components
        };
    }

    static section(contentOrComponents, accessory = null) {
        let components = [];
        if (typeof contentOrComponents === 'string') {
            components.push(this.textDisplay(contentOrComponents));
        } else if (Array.isArray(contentOrComponents)) {
            components = contentOrComponents;
        }

        const section = {
            type: 9, // Section
            components: components
        };

        if (accessory) {
            section.accessory = accessory;
        }

        return section;
    }

    static textDisplay(content) {
        return {
            type: 10, // TextDisplay
            content: content
        };
    }

    static thumbnail(url) {
        return {
            type: 11, // Thumbnail
            media: {
                url: url
            }
        };
    }

    static button(label, customId, style = 2) {
        return {
            type: 2, // Button
            style: style,
            custom_id: customId,
            label: label
        };
    }

    static actionRow(components = []) {
        return {
            type: 1, // ActionRow
            components: components
        };
    }

    static mediaGallery(items = []) {
        return {
            type: 12, // Media Gallery
            items: items
        };
    }
}

module.exports = V2Builder;
