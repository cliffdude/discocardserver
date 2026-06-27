// Card Status Page JavaScript
// Handles card reader input and API integration for card status lookup

document.addEventListener('DOMContentLoaded', function() {
    const cardInput = document.getElementById('cardInput');
    const loading = document.getElementById('loading');
    const errorMessage = document.getElementById('errorMessage');
    const statusDisplay = document.getElementById('statusDisplay');
    const itemsSection = document.getElementById('itemsSection');
    const summarySection = document.getElementById('summarySection');

    // Keep focus on the card input field at all times (kiosk mode)
    function keepFocus() {
        cardInput.focus();
    }

    // Keep focus on input field
    document.addEventListener('click', function(e) {
        if (e.target !== cardInput) {
            keepFocus();
        }
    });

    // Handle card input
    cardInput.addEventListener('keydown', function(e) {
        console.log('Key pressed:', e.key);
        if (e.key === 'Enter') {
            const cardNumber = cardInput.value.trim();
            console.log('Enter pressed, card number:', cardNumber);
            if (cardNumber) {
                lookupCardStatus(cardNumber);
            }
        }
    });

    // Also handle input changes for card readers that don't send Enter key
    let inputTimeout;
    cardInput.addEventListener('input', function() {
        clearTimeout(inputTimeout);
        inputTimeout = setTimeout(function() {
            const cardNumber = cardInput.value.trim();
            if (cardNumber && cardNumber.length > 3) {
                lookupCardStatus(cardNumber);
            }
        }, 500); // Wait 500ms after last input before processing
    });

    // Initial focus
    keepFocus();

    // Function to lookup card status
    async function lookupCardStatus(cardNumber) {
        console.log('lookupCardStatus called with:', cardNumber);
        // Reset display
        hideError();
        showLoading();
        hideResults();

        try {
            const apiUrl = `/api/cardstatus?cardnum=${encodeURIComponent(cardNumber)}`;
            console.log('Fetching from:', apiUrl);
            const response = await fetch(apiUrl);
            console.log('Response status:', response.status);
            
            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            const data = await response.json();

            console.log('Response data:', data);
            if (data.error) {
                console.log('Error in response:', data.error);
                showError(data.error);
                cardInput.value = '';
                return;
            }

            // Display the card information
            console.log('Displaying card status');
            displayCardStatus(data);
            
            // Clear input for next card
            cardInput.value = '';
            
        } catch (error) {
            console.error('Error looking up card status:', error);
            showError('Failed to retrieve card status. Please try again.');
            cardInput.value = '';
        } finally {
            hideLoading();
            // Keep focus on input for next card
            setTimeout(keepFocus, 100);
        }
    }

    // Display card status information
    function displayCardStatus(data) {
        // Update card information
        document.getElementById('cardNumber').textContent = data.cardNumber || '-';
        document.getElementById('mesaNumber').textContent = data.mesaNumber || '-';
        document.getElementById('currentStatus').textContent = data.status || '-';

        // Update photo
        const photoContainer = document.getElementById('photoContainer');
        if (data.photoUrl) {
            photoContainer.innerHTML = `<img src="${data.photoUrl}" alt="Card Photo" onerror="this.parentElement.innerHTML='<div class=\\'photo-placeholder\\'>Photo not available</div>'">`;
        } else {
            photoContainer.innerHTML = '<div class="photo-placeholder">No photo available</div>';
        }

        // Check for special status conditions
        const status = data.status || '';
        const minConsumption = data.minimumConsumption || 0;

        // Status = 2 is "Pago" - display PAGO message
        if (status === 'Pago') {
            itemsSection.innerHTML = '<div class="special-status-message pago">PAGO</div>';
            itemsSection.style.display = 'block';
            summarySection.style.display = 'none';
            statusDisplay.classList.add('active');
            setTimeout(hideResults, 5000);
            return;
        }

        // Status = 4 is "Ativado" AND minConsumption = 0 - display S/ CONSUMO message
        if (status === 'Ativado' && minConsumption === 0) {
            itemsSection.innerHTML = '<div class="special-status-message sem-consumo">S/ CONSUMO</div>';
            itemsSection.style.display = 'block';
            summarySection.style.display = 'none';
            statusDisplay.classList.add('active');
            setTimeout(hideResults, 5000);
            return;
        }

        // Normal display - restore items section structure and show items table
        itemsSection.innerHTML = `
            <div class="panel-title">Card Items</div>
            <table class="items-grid">
                <thead>
                    <tr>
                        <th>Description</th>
                        <th>Quantity</th>
                        <th>Total</th>
                    </tr>
                </thead>
                <tbody id="itemsTableBody">
                </tbody>
            </table>
        `;
        
        const itemsTableBody = document.getElementById('itemsTableBody');
        
        if (data.items && data.items.length > 0) {
            data.items.forEach(item => {
                const row = document.createElement('tr');
                // Format quantity - if it's a whole number, show as integer, otherwise show with 2 decimals
                const qty = item.quantity || 0;
                const formattedQty = Number.isInteger(qty) ? qty : qty.toFixed(2);
                row.innerHTML = `
                    <td>${item.description || '-'}</td>
                    <td>${formattedQty}</td>
                    <td>€${(item.total || 0).toFixed(2)}</td>
                `;
                itemsTableBody.appendChild(row);
            });
            itemsSection.style.display = 'block';
        } else {
            itemsSection.innerHTML = '<div class="panel-title">Card Items</div><p style="color: #a0a0a0; text-align: center; padding: 20px;">No items found for this card</p>';
            itemsSection.style.display = 'block';
        }

        // Update summary
        document.getElementById('totalAmount').textContent = `€${(data.totalAmount || 0).toFixed(2)}`;
        document.getElementById('minimumConsumption').textContent = `€${(data.minimumConsumption || 0).toFixed(2)}`;
        
        // Calculate balance
        const balance = (data.totalAmount || 0) - (data.minimumConsumption || 0);
        const balanceElement = document.getElementById('balance');
        balanceElement.textContent = `€${balance.toFixed(2)}`;
        
        // Color code the balance
        if (balance >= 0) {
            balanceElement.style.color = '#4caf50'; // Green
        } else {
            balanceElement.style.color = '#ff4444'; // Red
        }

        summarySection.style.display = 'block';

        // Show the status display
        statusDisplay.classList.add('active');

        // Clear card data after 5 seconds
        setTimeout(hideResults, 5000);
    }

    // UI Helper functions
    function showLoading() {
        loading.classList.add('active');
    }

    function hideLoading() {
        loading.classList.remove('active');
    }

    function showError(message) {
        errorMessage.textContent = message;
        errorMessage.classList.add('active');
    }

    function hideError() {
        errorMessage.classList.remove('active');
        errorMessage.textContent = '';
    }

    function hideResults() {
        statusDisplay.classList.remove('active');
        itemsSection.style.display = 'none';
        summarySection.style.display = 'none';
    }
});